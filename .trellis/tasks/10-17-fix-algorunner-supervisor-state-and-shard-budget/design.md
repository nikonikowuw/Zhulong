# Design — algorunner 监督器状态漂移、IPC 分片预算与并发健壮性

## 1. 范围与文件

本设计只改两个文件（外加两个新测试文件）：

| 文件 | 改动 |
| --- | --- |
| `internal/algorunner/supervisor.go` | R1 / R3 / R4 / R6 |
| `internal/algorunner/process.go` | R2 / R5 / R7 |
| `internal/algorunner/recovery.go` | R4（`RecoveryOptions.normalize()` 单一来源） |
| `internal/algorunner/supervisor_internal_test.go`（新） | AC1 白盒不变量测试 |
| `internal/algorunner/supervisor_test.go`（追加） | AC6 / AC7 / AC8 / AC10 |
| `internal/algorunner/process_test.go`（改写/追加） | AC3 / AC4 / AC5 / AC9 / AC11 |

不触碰 `internal/algoipc`、`internal/algorithm`、任何 native 文件、线协议常量。

---

## 2. R1 — watcher 退役与状态写入解耦

### 问题定位

`StopAlgorithm` 的现有结构已经承认「map 里可能已经不是自己持有的 watcher」，却没有让状态写入跟随同一判断：

```go
s.mu.Lock()
if s.watchers[algoID] == w { delete(s.watchers, algoID) }   // ← 判断了
s.mu.Unlock()
if s.store != nil { UpdateAlgorithmStatus(ctx, algoID, StatusDisabled) }  // ← 没跟着判断
```

可达交错（已在审查中推导）：`superviseLoop` 崩溃分支判定 `guard.ShouldRollback` 成立后，先 `delete(s.watchers, ID)`，再由 `rollbackToOld` 注册新 watcher；若 `StopAlgorithm` 在此**之前**已取到旧 watcher，则 `stopProcess` 作用在已死进程上、`<-doneCh` 立即返回，随后写入 `DISABLED`——而新 watcher 的进程正在运行。

### 设计

抽出唯一的退役入口，把「是否移除」变成返回值，状态写入以它为条件：

```go
// retireWatcher removes w from the watcher registry if it is still the watcher
// registered for algoID, reporting whether it actually removed w.
//
// The return value is the guard for status writes: when a rollback or upgrade
// has already re-registered the same algorithm ID, the registered watcher is a
// different (live) process, so this caller must not touch the persisted status.
func (s *Supervisor) retireWatcher(algoID string, w *workerWatcher) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watchers[algoID] != w {
		return false
	}
	delete(s.watchers, algoID)
	return true
}
```

`StopAlgorithm` 末尾：

```go
if s.retireWatcher(algoID, w) && s.store != nil {
	if err := s.store.UpdateAlgorithmStatus(ctx, algoID, StatusDisabled); err != nil {
		s.logger.Error(...)
	}
}
```

### 为什么这样可测

不变量被收敛到一个纯函数式的判断上，因此 AC1 可以**确定性地**用白盒测试验证（注册 w2，用 w1 调 `retireWatcher`，断言返回 false 且 store 未被写入），不需要依赖真实时序去撞那个几十微秒的窗口。真实的并发场景再补一个黑盒冒烟测试（`-race` 下多轮），作为交叉验证。

`ResetAlgorithm` / `StopAll` 的既有语义不变：前者本来就只在 `StatusCrashLoop` 分支写 `DISABLED`，后者不写状态。

---

## 3. R2 — 预算驱动的分片

### 关键事实（实测）

- 512 维 float32 特征 JSON 序列化后 ≈ **12.3 bytes/float**，单条 item ≈ **6.4KB**。
- `itemsPerShard = 10` 在真实数据下产出 **64204~65373** 字节，上限 65536 → 余量仅 **0.25%~2.0%**。
- 超限时**返回 error**，`BroadcastSync` 直接中止 → 一片都不发 → runner 的 `ShardedSyncContext` 永远不 `is_complete()` → 冷启动底库为空。

### 设计：按测量出的字节数贪心装箱

JSON 序列化是**可加**的：一个 shard 的字节数 = 头部 + `[` + 逗号连接的各 item + `]`，而每个 item 的序列化结果与其单独序列化完全一致。因此可以「测量一次 → 算术装箱 → 校验一次」，避免在装箱循环里反复 marshal（否则 10000 张人脸会产生数百 MB 的临时 JSON）。

```go
// shardBudget caps a single shard at 90% of the protocol limit. The remaining
// headroom absorbs the shard header and any drift between the measured item
// sizes and the final marshalled payload.
const shardBudget = MaxIpcPayloadSize * 9 / 10 // 58982 bytes
```

算法：

1. 逐条 `json.Marshal(item)` 得到 `itemSizes[i]`（一次 O(n) 小对象序列化）。
2. `overhead` = 序列化一个 `ShardIndex/ShardTotal` 取 6 位数的空 items 分片所得长度（覆盖最多 999999 片的头部宽度）。
3. 贪心装箱：累计满足 `overhead + Σsizes + (n-1) <= shardBudget` 即继续收，否则封片。
4. 单条满足 `overhead + size > shardBudget` 时**立即返回错误**，错误信息携带 `face_id`（可定位到具体人员）。
5. 用真实 `shard_index` / `shard_total` 序列化每片，并做一次防御性校验：`len(payload) <= MaxIpcPayloadSize`。

### 语义保持

- 空输入仍返回 1 个空 items 分片（既有行为与测试依赖）。
- 每片 items 的**顺序与内容**与输入完全一致（`AC5`），保证 native 侧聚合后底库与 DB 一致。
- `shard_total` 与实际片数严格相等，`shard_index` 从 0 连续递增。

---

## 4. R3 — resolver 移出写锁

### 问题定位

`resolveBrokerSocksLocked` 的 3 个调用点全部在写锁内，而实现侧要做 2 次 DB 查询 + 逐相机路径解析。期间 `GetStatus`（RLock）阻塞，`Service.ListAlgorithms` 对每个算法调一次 `GetStatus` → HTTP 列表接口出现随规模放大的延迟尖刺。

### 设计：解析在锁外，注册在锁内 + 二次校验

```go
func (s *Supervisor) StartAlgorithm(ctx context.Context, algo PackageMeta) error {
	// 1. 快路径：锁内只做存在性判断，顺便取出 resolver 引用。
	s.mu.RLock()
	_, exists := s.watchers[algo.ID]
	resolver := s.resolver
	s.mu.RUnlock()
	if exists {
		return errors.New("algorithm worker is already running or supervised")
	}

	// 2. 锁外做 IO（DB + 订阅同步）。
	brokerSocks, err := s.resolveBrokerSocks(ctx, resolver, algo.ID)
	if err != nil {
		return err
	}

	// 3. 锁内注册：二次校验兜底解析期间产生的并发注册。
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.watchers[algo.ID]; exists {
		return errors.New("algorithm worker is already running or supervised")
	}
	... // 构建 watcher + 启动进程 + 注册
}
```

`resolveBrokerSocksLocked` 改名为 `resolveBrokerSocks`（不再要求持锁），签名显式接收 resolver 快照，避免「函数名承诺持锁、实际不持锁」的误导。

**并发注册的语义说明**：二次校验失败时，resolver 可能已经把该包的订阅关系写进去了。这不是泄漏——订阅是「为该算法包保活某路 AI 流」的幂等集合语义，而注册成功的那次调用拥有同一份订阅。因此只需丢弃本地构建的 watcher，无需回滚订阅。

### `UpgradeAlgorithm` 的锁重构

现状是 `s.mu.Lock(); defer s.mu.Unlock()` 覆盖了 resolve + 进程启动 + 回滚分支。重构为「解析在锁外，注册在锁内」，并让回滚分支在**锁外**调用（`rollbackToOld` 自己取锁）：

```txt
1. 锁内：摘下旧 watcher（delete），解锁
2. 锁外：停止旧进程 + <-oldWatcher.doneCh
3. 锁外：resolve(newAlgo.ID)
   └─ 失败 → 直接 return s.rollbackToOld(ctx, oldAlgo)   // 内部自行解析+取锁
4. 锁内：二次校验 + 构建 watcher + 启动进程 + 注册 + go superviseLoop
   └─ 启动失败 → 解锁后 return s.rollbackToOld(ctx, oldAlgo)
```

⚠️ 步骤 4 的失败分支**不能**用 `defer s.mu.Unlock()` 覆盖，否则 `rollbackToOld` 会自死锁。实现上用显式 `s.mu.Unlock()`。

`rollbackToOld` 拆成两层：外层（锁外解析）→ 内层 `rollbackToOldLocked(ctx, oldAlgo, brokerSocks)`（锁内注册）。解析失败时内层不启动半瘫进程，直接向上返回错误——保持既有行为。

`superviseLoop` 的回滚分支本来就先解锁再调用 `rollbackToOld`，无需改动。

---

## 5. R4 — `RecoveryOptions` 归一化单一来源

把归一化从 `NewRecoveryStateMachine` 内联逻辑提升为 `RecoveryOptions` 的方法，由 `NewSupervisor` 与 `NewRecoveryStateMachine` 共同调用：

```go
// normalize fills in defaults for unset or non-positive fields.
//
// It is the single source of truth for recovery option defaults: NewSupervisor
// stores the normalized value because superviseLoop feeds StableRunningThreshold
// straight into time.NewTicker, which panics (not errors) on a non-positive
// interval, and feeds RollbackGuardWindow into time.NewTimer, where a zero
// window silently disables rollback observation.
func (o RecoveryOptions) normalize() RecoveryOptions {
	if o.InitialBackoff <= 0 { o.InitialBackoff = DefaultBaseBackoff }
	if o.MaxBackoff <= 0 { o.MaxBackoff = DefaultMaxBackoff }
	if o.CrashWindow <= 0 { o.CrashWindow = DefaultCrashWindow }
	if o.MaxCrashes <= 0 { o.MaxCrashes = DefaultMaxCrashesInWindow }
	if o.StableRunningThreshold <= 0 { o.StableRunningThreshold = DefaultStableResetDuration }
	if o.RollbackGuardWindow <= 0 { o.RollbackGuardWindow = DefaultRollbackGuardWindow }
	return o
}
```

`NewSupervisor` 中 `opts: opts.normalize()`；`NewRecoveryStateMachine` 中删除内联块改为 `opts = opts.normalize()`。

⚠️ 值接收者 + 返回新值，避免 `DefaultRecoveryOptions()` 的共享状态被就地修改。

---

## 6. R5 — `.env` 解析错误不再静默

```go
if p.config.EnvFile != "" {
	loaded, err := LoadDotEnv(p.config.EnvFile)
	if err != nil {
		// 文件不存在已由 LoadDotEnv 转为空 map，因此这里的错误都是真错误
		// （例如超长行触发 bufio.Scanner 的 ErrTooLong）。不吞错、不阻断启动。
		p.logger.Warn("failed to load runner dotenv file",
			zap.String("path", p.config.EnvFile), zap.Error(err))
	}
	for k, v := range loaded {
		envMap[k] = v
	}
}
```

语义不变：文件缺失仍视为可选（`LoadDotEnv` 返回空 map + nil）；只新增可观测性。

---

## 7. R6 — 崩溃重启重新解析 broker 列表

现有注释声明「崩溃重启路径无 ctx，复用缓存的 brokerSocks」。`ctx` 实际可由 `context.Background()` 获得，且 `R3` 之后解析不再需要写锁，因此改为每次重启重新解析：

```txt
崩溃 → 退避等待 → 锁外 resolve(w.algo.ID)
  ├─ 成功 → 更新 w.brokerSocks，用新列表启动
  └─ 失败 → 记录 Warn，回退到 w.brokerSocks（不退化、不因解析失败进入 CRASH_LOOP）
```

关键点：**解析必须在取 `w.mu` 之前完成**。`w.mu` 会被 `GetStatus` / `ResetAlgorithm` / `stopProcess` 获取，在持锁期间做 DB IO 会把 R3 刚修掉的阻塞问题搬到 watcher 层。

`w.brokerSocks` 仅由 `superviseLoop` 读写（初始值来自 `StartAlgorithm` 且启动后不再变更），因此赋值不需要额外同步；为清晰起见在写入处加注释说明该单一写者约束。

---

## 8. R7 — 独立进程组 + 整组终止

### 启动

```go
cmd := exec.Command(p.config.RunnerPath, args...)
// 独立进程组：让 SIGTERM/SIGKILL 能覆盖 runner 派生的全部子进程，
// 避免孤儿进程继续持有 broker socket 与共享内存。
cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
```

### 停止

保持「SIGTERM → 超时 → SIGKILL」两段式，把信号目标从单进程改为进程组，并对不支持进程组的场景回退：

```go
// signalRunner 向 runner 所在进程组发送信号；若进程组不可用（例如 Setpgid
// 未生效或进程已消失），回退到对进程本身发信号。
func (p *RunnerProcess) signalRunner(sig syscall.Signal) error {
	pid := p.cmd.Process.Pid
	if err := syscall.Kill(-pid, sig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return p.cmd.Process.Signal(sig)
	}
	return nil
}
```

⚠️ `syscall.Kill(-pid, ...)` 依赖 `Setpgid` 成功，因此仅在 `Start` 已设置 `SysProcAttr` 时使用；`ESRCH`（组不存在）静默忽略，与既有 `os.ErrProcessDone` 处理一致。

`os/exec` 与 `syscall` 均为已导入包，无新增依赖。项目目标平台为 Linux（既有代码已使用 `syscall.SIGTERM`），无新增平台约束。

---

## 9. 测试策略

| AC | 类型 | 关键手法 |
| --- | --- | --- |
| AC1 | 白盒（同包） | 注册 w2 后用 w1 调 `retireWatcher`，断言返回 false 且 store 未被写入；再补 `-race` 黑盒并发冒烟 |
| AC2 | 黑盒 | 既有 `RestartAlgorithm` / `Enable/Disable` 路径 + 新增正常 Stop 断言 DISABLED |
| AC3 | 表驱动 | **真实量级**特征（512 维 L2 归一化、约 12.3 B/float）+ 26 字符 ULID + 中文姓名 + 真实图片文件名 |
| AC4 | 表驱动 | 构造超预算单条（超长 `person_name`），断言错误信息含 face_id |
| AC5 | 黑盒 | 打散后重组，断言与输入逐字段相等、`shard_total` 一致、`shard_index` 连续 |
| AC6 | 并发 | 阻塞式 resolver 制造窗口，主协程断言 `GetStatus` 在 200ms 内返回 |
| AC7 | 并发 | 阻塞式 resolver + `sync.WaitGroup` 栅栏，断言恰好一个 `StartAlgorithm` 成功 |
| AC8 | 表驱动 | 6 个字段分别取 `0` / 负数，断言不 panic 且能正常 Start/Stop |
| AC9 | 黑盒 | `.env` 含 >64KB 行，断言 Warn 日志出现且进程仍能启动 |
| AC10 | 黑盒 | 可变 mock resolver（加锁），崩溃重启后断言第二次 `--broker` 为新值 |
| AC11 | 黑盒（Linux） | 脚本 `sleep 30 &` 并落盘子进程 PID，Stop 后断言 `kill(pid, 0)` 返回 ESRCH |

⚠️ AC3 必须能让**旧实现失败**：旧实现下 10 条真实数据会超 64KB，而测试断言「每片 <= shardBudget」会失败。这一点在验收时用 `git stash` 对旧实现跑一次确认。

## 10. 回滚与兼容性

- 全部改动在包内，无 schema 变更、无线协议变更、无 API 契约变更 → 可直接 `git revert` 单次提交。
- `CreateGallerySyncShards` 的输出片数会变化（每片从 10 条变为按字节预算的 ~9 条），但 native 侧按 `shard_total` 聚合，属于纯内部行为，兼容。
- 风险点：R3 的锁重构改变了 `StartAlgorithm` / `UpgradeAlgorithm` 的锁边界。用既有 8 个 supervisor 测试 + 新增 AC6/AC7 覆盖，并强制 `-race`。
