# 实施记录

## 改动清单

### 后端

| 文件 | 改动 |
| --- | --- |
| `internal/systemtime/statemachine.go` | 新增 `ActionKind` / `SyncOutcome`；`ProcessNTPResult` 返回 `(SyncOutcome, error)`；panic 复核时 outcome 与 error 一并返回 |
| `internal/systemtime/service.go` | `probeOnce` 只在 `ActionStepped` / `ActionPanicReview` 时写审计；稳态 slew 降级为 `Debug`；新增 `recordSystem` / `recordProbeFailure` / `recordProbeSuccess`（边沿去抖）；内容为 `SyncNow` 适配双返回值 |
| `internal/systemtime/model.go` | 新增 `ActionTimeClockStep` / `ActionTimeNTPSyncFailed` / `ActionTimeNTPRecovered` |
| `internal/database/migrations/000017_purge_ntp_heartbeat_audit.{up,down}.sql` | 删除 `action='time.ntp_sync' AND username='system'`；down 为 no-op |
| `internal/systemtime/probe_audit_test.go` | 5 个新用例覆盖稳态/slew、step、panic、失败跃迁、驱动层失败、nil recorder |
| `internal/systemtime/statemachine_test.go` | 6 处调用点改双返回值并断言 `Kind` |
| `internal/database/store_test.go` | `TestMigration000017_PurgeNTPHeartbeat` |

### 前端

| 文件 | 改动 |
| --- | --- |
| `web/src/features/audit/components/AuditTable.tsx` | `actionOptions` 增三个新 action |
| `web/src/features/audit/locales/{zh-Hans,zh-Hant,en}.json` | 补三个新 action 文案 |

### 规范

| 文件 | 改动 |
| --- | --- |
| `.trellis/spec/backend/logging-guidelines.md` | §4 新增第 13 条「禁止心跳型审计」；新增 Scenario「Background Workers and the Audit Trail」 |

## 关键决策

1. **`time.ntp_sync` 语义收窄为人工操作**。后台不再写这个 action，因此它天然变为「有人按了立即对时」的信号。
2. **panic 复核记 `time.clock_step` + failed**，而不是新造一个 action —— 它确实是「一次时钟跳变校正」的复核阶段，
   只是尚未落地。这样按 action 筛选时，墙钟跳变的全过程都在同一列下。
3. **失败/恢复用边沿检测而非次数限制**。次数限制（如「每小时最多一条」）在真正的短时抖动下仍会漏记恢复事件；
   边沿检测直接对应「状态跃迁」这个语义。
4. **`down.sql` 写成 no-op**。重建的行无法与真实发生的审计记录区分，伪造审计证据比缺记录更危险。

## 已知遗留（非本任务）

- `internal/record` 的 `TestServiceEnqueuesSnapshotOnRecord` 在 HEAD 上即有 data race
  （`fakeSnapshotProvider.Snapshot` 写 `options.Boxes` 与测试主 goroutine 读之间无同步）。
- `internal/engine` 的 `TestBridgeIntegration_AIConsumerAndBrokerPath` 在 HEAD 上即失败
  （`native engine acquire failed: I/O communication error (code -11)`）。

两者均已用 `git stash` + 干净 HEAD 复现确认，与本次改动无关。

## 追加：清理两处门禁阻断项（用户要求）

在确认审计改动全绿后，追加修复原先被记为「已知遗留」的两项，并顺带修掉同一批
macOS 平台缺陷。判据是：这些都是**真实缺陷**，不是环境噪声。

### A. `internal/record` 数据竞争

- 现象：`TestServiceEnqueuesSnapshotOnRecord` 在 `-race` 下报 race。
- 根因：`fakeSnapshotProvider.Snapshot` 写 `options` 时持锁，但测试主 goroutine
  直接裸读 `provider.options`。写侧已正确加锁，缺的是读侧。
- 修复：新增 `lastOptions()` 访问器（持同一把 `mu`，并复制 `Boxes` 切片底层数组），
  两处断言改用它。
- 验证：`go test -race -count=3 ./internal/record` 全绿。

### B. `internal/engine` 的 `AIConsumerAndBrokerPath` (code -11)

- 现象：`Acquire(..., ConsumerAI)` 返回 `Zhulong_ERR_IO`。
- 根因：`FrameBroker::start` 的目录锁集合硬编码 `{base_dir, "/dev/shm"}`。`/dev/shm`
  是 Linux 专有挂载点，macOS 不存在，`BrokerResourceLock::acquire` 在 `stat` 阶段失败 →
  `resource_lock` 为空 → 整个 broker 创建返回 false → 上层翻译成 `ERR_IO`。
  Go 侧对应实现（`internal/algorunner/orphan_other.go`）在非 Linux 平台本就是 no-op，
  也就是说 macOS 上根本不存在需要互斥的清扫方。
- 修复：
  - `BrokerResourceLock::resource_dirs(base_dir)` 集中定义目录集合，`kShmSweepDir`
    在非 Linux 下为 `nullptr`，不再把不存在的目录纳入锁集。
  - `frame_broker.cpp` 与 `native/tests/frame_broker_test.cpp` 都改用该单一来源，
    避免生产代码与测试各自硬编码而漂移。
- 连带收益：`ZhulongRtspIntegration`（native）由失败转为通过。

### C. 顺带修掉的三个 macOS native 失败（24/24 现全绿）

| 测试 | 根因 | 修复 |
| --- | --- | --- |
| `ZhulongDmaBufAllocator` | `DmaBuffer::sync_start/sync_end` 的 `fd_ < 0` 检查在 `#if defined(__linux__)` **内部**，非 Linux 分支直接 `return true` —— 空对象声称同步成功 | 把 fd 检查提到条件编译之外：不能对自己不拥有的 fd 报告成功，这与平台无关 |
| `ZhulongIpcSocketFd` | macOS `AF_UNIX` 默认 `SO_SNDBUF` 仅 8KB（Linux 约 208KB），单次 `sendmsg` 发满 64KB 报文时撞上生产代码的 50ms `SO_SNDTIMEO`，把协议内合法报文误判为发送失败 | accept 后把 `SO_SNDBUF` 设为 `MAX_IPC_PAYLOAD_SIZE * 2`；只调上限，不放松 50ms 慢消费者语义 |
| `ZhulongFaceFeatureExtract` | `FacePluginLoader` 的候选插件路径全部硬编码 `.so`，macOS 产物是 `.dylib`，插件永远加载不到 → 全黑图断言期望 `NOT_FOUND` 却拿到 `UNSUPPORTED` | 引入 `kPluginExt`（`__APPLE__` → `.dylib`，否则 `.so`），候选名统一由它拼装 |

### 门禁结果

```
make native-test     100% tests passed, 24/24
make go-check        all packages ok + bridge integration passed
make frontend-check  551 tests passed, lint clean, build ok
make api-docs        regenerated
```
