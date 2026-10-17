# Implement — algorunner 监督器状态漂移、IPC 分片预算与并发健壮性

执行顺序按「依赖关系 + 风险递增」排列：先做无锁语义争议的纯函数改动（R4/R5/R2），再做测试可白盒验证的状态修复（R1），最后做锁边界重构（R3/R6）与进程组（R7）。

**每个步骤都必须先写失败测试，再改实现。** 每步完成后跑一次局部验证。

---

## Step 0 — 基线确认

- [ ] 记录当前基线，确认工作区干净：

```bash
git status --porcelain          # 期望空
python3 native/scripts/build.py go test -race ./internal/algorunner 2>&1 | tail -5
```

- [ ] **验证**：全部测试通过（`ok ... internal/algorunner`）。这是「修复不引入回归」的比较基准。

---

## Step 1 — R4：`RecoveryOptions.normalize()`

**先写测试**（`recovery_test.go` 追加）：

- [ ] 表驱动 `TestRecoveryOptionsNormalize`：对 `StableRunningThreshold` / `RollbackGuardWindow` / `InitialBackoff` / `MaxBackoff` / `CrashWindow` / `MaxCrashes` 分别取 `0` 与 `-1`，断言 `NewSupervisor(...)` 不 panic，且 `StartAlgorithm` + `StopAlgorithm` 能正常完成。
- [ ] **验证（必须先失败）**：`StableRunningThreshold = 0` 的用例在旧实现下应 panic（`non-positive interval for NewTicker`）。若没 panic，说明测试没跑到 `superviseLoop`，需修正。

**再改实现**：

- [ ] `recovery.go`：新增 `func (o RecoveryOptions) normalize() RecoveryOptions`，把 `NewRecoveryStateMachine` 的内联归一化块整体搬进去（6 个字段的 `<= 0` 判断，值接收者 + 返回新值）。
- [ ] `recovery.go`：`NewRecoveryStateMachine` 改为 `opts = opts.normalize()`。
- [ ] `supervisor.go`：`NewSupervisor` 中 `opts: opts.normalize()`。
- [ ] **验证**：`python3 native/scripts/build.py go test -race ./internal/algorunner -run 'TestRecovery'`

⚠️ 值接收者，禁止就地修改入参（`DefaultRecoveryOptions()` 的返回值会被多处共享）。

---

## Step 2 — R5：`.env` 错误不再静默

**先写测试**（`process_test.go` 追加）：

- [ ] `TestRunnerProcessReportsDotEnvParseFailure`：写一个含 >64KB 单行的 `.env`，用 `observer` logger 捕获日志，断言出现 Warn 且进程仍正常启动/停止。
- [ ] **验证（必须先失败）**：旧实现下断言不到 Warn。

**再改实现**：

- [ ] `process.go` `Start()`：把 `if loaded, err := LoadDotEnv(...); err == nil` 改为显式处理 `err`，`err != nil` 时 `p.logger.Warn("failed to load runner dotenv file", zap.String("path", ...), zap.Error(err))`，随后无条件遍历 `loaded`（nil map 遍历安全）。
- [ ] **验证**：`... -run 'TestRunnerProcess.*DotEnv|TestLoadDotEnv'`，确认既有的 `TestRunnerProcessMissingDotEnvIsOptional` 与 `TestRunnerProcessInjectsDotEnvEndToEnd` 仍通过。

---

## Step 3 — R2：预算驱动的分片

**先写测试**（`process_test.go` 改写 + 追加）：

- [ ] 新增 `testGalleryFeature` 辅助函数：生成 512 维 **L2 归一化** float32（分量 ~N(0, 1/512) 后归一化），序列化后约 12.3 bytes/float。
- [ ] 改写 `TestCreateGallerySyncShards_MultiShardUnder64KB`：数据改用真实量级（真实特征 + 26 字符 ULID 形态的 face_id/person_id + 中文姓名 + 真实图片文件名），断言每片 `<= shardBudget`（同包测试可直接引用常量）。
- [ ] 新增 `TestCreateGallerySyncShards_SingleItemExceedsBudget`：超长 `PersonName` 使单条超预算，断言返回 error 且 `strings.Contains(err.Error(), faceID)`。
- [ ] 新增 `TestCreateGallerySyncShards_RoundTripIntegrity`：构造 137 条（非整片数），断言拼接后 items 与输入逐字段相等、`shard_total` == 实际片数、`shard_index` 为 0..n-1 连续。
- [ ] 新增 `TestCreateGallerySyncShards_ShardBudgetHoldsForRealisticGallery`：批量 200 条真实数据，断言**全部**片 `<= shardBudget` 且无 error（旧实现必然失败）。
- [ ] **验证（必须先失败）**：`... -run 'TestCreateGallerySyncShards'`，确认新增用例在旧实现下失败（10 条真实数据 > 64KB）。

**再改实现**（`process.go`）：

- [ ] 新增常量：

```go
// shardBudget caps a single gallery sync shard at 90% of the protocol limit so
// the shard header and marshalling drift cannot push a payload past
// MaxIpcPayloadSize. Measured sizes: a 512-dim L2-normalized float32 embedding
// serializes to ~12.3 bytes per component (~6.3KB per item), so the old fixed
// 10-items-per-shard packing left under 1% headroom on real data.
const shardBudget = MaxIpcPayloadSize * 9 / 10
```

- [ ] 重写 `CreateGallerySyncShards`：
  1. 空输入分支保持不变（1 片、空 items）。
  2. 逐条 `json.Marshal(item)` → `itemSizes []int`（一次 O(n) 小对象序列化，避免装箱循环里重复 marshal）。
  3. 计算 `overhead`：序列化一个 `ShardIndex: 0, ShardTotal: 999999, Items: []GallerySyncItem{}` 的分片取 `len()`，覆盖最多 6 位片号的头部宽度。
  4. 贪心装箱：`overhead + sum(sizes) + (n-1)`（逗号）`<= shardBudget` 则继续收，否则封片。
  5. 单条 `overhead + size > shardBudget` → `return nil, fmt.Errorf("gallery sync item %s exceeds the shard budget (%d bytes > %d)", item.FaceID, ...)`。
  6. 用真实 `shard_index` / `shard_total` 序列化每片，并保留防御性校验 `len(payload) > int(MaxIpcPayloadSize)` → error（预算算错时的最后一道闸）。
- [ ] 更新 `CreateGallerySyncShards` 上方注释：删除「at most 10 items」的旧描述，说明预算驱动策略与实测依据。
- [ ] **验证**：`... -run 'TestCreateGallerySyncShards|TestGalleryBroadcaster'`

⚠️ 不得改动 `GallerySyncShardPayload` 的 JSON 字段名与 `MaxIpcPayloadSize`（跨语言契约）。

---

## Step 4 — R1：`retireWatcher` 与状态写入解耦

**先写测试**（新增 `supervisor_internal_test.go`，`package algorunner` 白盒）：

- [ ] `TestRetireWatcherReportsWhetherItRemoved`：注册 `w1` → 用 `w1` 调用返回 `true` 且 map 中该 ID 消失；再注册 `w2` → 用 `w1` 调用返回 `false` 且 `w2` **仍在** map 中。
- [ ] `TestStopAlgorithmDoesNotDisableAReplacedWatcher`：注册 `w2` 后，用 `w1` 走「停止 + 退役」路径，断言 `store` 中该 ID 的状态**未被写入 DISABLED**。
- [ ] **验证（必须先失败）**：第二条在旧实现下失败（旧实现无条件写 DISABLED）。

**再改实现**（`supervisor.go`）：

- [ ] 新增 `retireWatcher(algoID string, w *workerWatcher) bool`（含解释「为什么返回值是状态写入的守卫」的注释）。
- [ ] `StopAlgorithm` 末尾改为 `if s.retireWatcher(algoID, w) && s.store != nil { ... }`。
- [ ] `ResetAlgorithm` 中的 `if s.watchers[algoID] == w { delete(...) }` 改用 `retireWatcher`（保持行为等价，减少重复判断）。
- [ ] `StopAll` 保持不写状态（既有语义），仅确认无需改动。
- [ ] **验证**：`... -run 'TestRetireWatcher|TestStopAlgorithm|TestSupervisorReset|TestSupervisorRestartPreservesActiveStatus'`

**补充黑盒并发冒烟**（`supervisor_test.go`）：

- [ ] `TestSupervisorRollbackThenStopKeepsStatusConsistent`：短 `RollbackGuardWindow` + 版本敏感 runner 触发自动回滚，同时在回滚窗口内并发调 `StopAlgorithm`，多轮循环（≥5 轮）后断言「`GetStatus` 为 RUNNING 时 store 状态不为 DISABLED」。`-race` 下必须稳定通过。

---

## Step 5 — R3：resolver 移出写锁

**先写测试**（`supervisor_test.go` 追加）：

- [ ] 新增 `blockingBrokerPathResolver`：`SyncBrokerSubscriptions` 阻塞在可关闭的 channel 上，并记录被调用的 algorithmID。
- [ ] `TestGetStatusNotBlockedByBrokerResolution`：resolver 阻塞期间并发调 `GetStatus`，断言 200ms 内返回（旧实现下会阻塞至超时）。
- [ ] `TestConcurrentStartAlgorithmRegistersOnce`：resolver 阻塞 + `sync.WaitGroup` 栅栏，两个 goroutine 同时 `StartAlgorithm` 同一 ID，断言**恰好一个**返回 nil，另一个返回「already running」类错误。
- [ ] **验证（必须先失败）**：第一条在旧实现下超时失败。

**再改实现**（`supervisor.go`）：

- [ ] `resolveBrokerSocksLocked` → 重命名 `resolveBrokerSocks(ctx, resolver BrokerSubscriptionResolver, algoID string)`，删除「调用方必须持有 s.mu」的注释，改为说明「不持锁、允许 IO」。
- [ ] `StartAlgorithm` 改为「RLock 快路径 → 锁外 resolve → Lock 二次校验 + 注册」（见 design §4）。
- [ ] `UpgradeAlgorithm` 改为「锁内摘旧 watcher → 锁外停旧进程 + resolve → 锁内二次校验 + 启动 + 注册」；回滚分支在**解锁后**调用 `s.rollbackToOld`。
- [ ] `rollbackToOld` 拆为外层（锁外 resolve）+ `rollbackToOldLocked(ctx, oldAlgo, brokerSocks)`（锁内注册）。
- [ ] ⚠️ 检查所有提前返回路径都不再依赖 `defer s.mu.Unlock()`（否则回滚路径自死锁）。
- [ ] **验证**：`... -run 'TestGetStatusNotBlocked|TestConcurrentStartAlgorithm|TestSupervisor.*' -race`

---

## Step 6 — R6：崩溃重启重新解析 broker 列表

**先写测试**：

- [ ] 新增 `mutableBrokerPathResolver`（内部加锁，可 `Set(paths)`）。
- [ ] `TestSupervisorCrashRestartReResolvesBrokerList`：启动后 `Set` 新路径，`kill -9` runner，断言第二次调用记录的 `--broker` 为**新**路径。
- [ ] `TestSupervisorCrashRestartFallsBackWhenResolveFails`：重启前把 resolver 置为失败，断言仍使用缓存的旧路径启动且**不进入 CRASH_LOOP**。
- [ ] 更新既有 `TestSupervisorCrashRestartKeepsBrokerList` 的意图：其断言「复用固化列表」与 R6 冲突，改为断言「重启后仍能拿到可用的 broker 列表且不熔断」（保留防 exit 5 的核心意图）。
- [ ] **验证（必须先失败）**：第一条在旧实现下失败（拿到旧路径）。

**再改实现**（`supervisor.go` `superviseLoop` 崩溃重启分支）：

- [ ] 在退避等待之后、取 `w.mu` **之前**，调用 `s.resolveBrokerSocks(ctx, resolver, w.algo.ID)`（resolver 用 `s.mu.RLock()` 取快照）。
- [ ] 成功 → 更新 `w.brokerSocks`；失败 → `logger.Warn` 并回退到 `w.brokerSocks`。
- [ ] 删除「崩溃重启路径无 ctx」的过时注释，替换为「单一写者 + 失败回退」的说明。
- [ ] **验证**：`... -run 'TestSupervisorCrashRestart' -race`

---

## Step 7 — R7：独立进程组 + 整组终止

**先写测试**（`process_test.go` 追加，Linux only）：

- [ ] `TestRunnerProcessStopKillsProcessGroup`：runner 脚本 `sleep 30 & echo $! > pidfile; wait`，`Stop` 返回后读 pidfile，断言 `syscall.Kill(childPID, 0)` 返回 `ESRCH`。
- [ ] **验证（必须先失败）**：旧实现下子进程仍存活（`kill 0` 返回 nil）。

**再改实现**（`process.go`）：

- [ ] `Start()` 中 `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}`，附注释说明目的（覆盖 runner 派生子进程，避免孤儿持有 broker socket 与共享内存）。
- [ ] 新增 `signalRunner(sig syscall.Signal) error`：优先 `syscall.Kill(-pid, sig)`，`ESRCH` 静默忽略，其他错误回退 `p.cmd.Process.Signal(sig)`。
- [ ] `Stop()` 中的 SIGTERM 与 SIGKILL 两处改为调用 `signalRunner`，保持两段式时序与日志不变。
- [ ] **验证**：`... -run 'TestRunnerProcess' -race`，确认 `TestRunnerProcessLifecycleGracefulStop` / `TestRunnerProcessForceKillOnTimeout` 仍通过。

---

## Step 8 — 全量质量门禁

- [ ] `gofmt -l internal/algorunner` → 期望无输出。
- [ ] `python3 native/scripts/build.py go vet ./internal/algorunner`
- [ ] `python3 native/scripts/build.py go test -race -count=2 ./internal/algorunner`（`-count=2` 抓非确定性）
- [ ] `python3 native/scripts/build.py go test -race ./internal/algorithm ./internal/app`（确认调用方无回归）
- [ ] `make go-check`
- [ ] `git diff --stat` → 确认改动仅落在 `internal/algorunner/`（满足 AC13）。

---

## Step 9 — 反向验证（关键闸门）

- [ ] 对 Step 3 的分片测试执行反向验证，证明它真的能抓住旧 bug：

```bash
git stash push internal/algorunner/process.go
python3 native/scripts/build.py go test -race ./internal/algorunner -run TestCreateGallerySyncShards
git stash pop
```

- [ ] 期望：旧实现下新增的分片测试**失败**。若通过，说明测试数据仍不够真实，必须回到 Step 3 修正（这是 R2 未被发现的根本原因）。
- [ ] 同理对 Step 1（R4 panic）与 Step 4（R1 状态漂移）各做一次反向验证。

---

## Step 10 — 收尾

- [ ] 更新 `.trellis/spec/`（若本次确立了可复用的约定）：至少记录「监督器状态写入必须以 watcher 退役成功为条件」与「跨语言定长预算的序列化必须按测量字节装箱」两条。
- [ ] 按 conventional commit 提交：`fix(algorunner): ...`
- [ ] `task.py archive`

---

## 回滚点

| 回滚点 | 触发条件 | 操作 |
| --- | --- | --- |
| 每个 Step 独立 | 该步测试失败且无法在 30 分钟内定位 | `git checkout -- internal/algorunner/<file>` |
| Step 5 之后 | `-race` 出现新的死锁/竞态且非测试问题 | 单独 revert Step 5 的提交（锁重构） |
| 全部完成后 | 集成测试暴露行为回归 | `git revert <commit>`（单次提交，包内改动，无 schema/协议变更） |
