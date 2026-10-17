# 修复 algorunner 监督器状态漂移、IPC 分片预算与并发健壮性缺陷

## Goal

修复 `internal/algorunner` 的 5 个已证实缺陷，使算法包监督器的「DB 状态 ↔ 实际进程状态」不再漂移，底库冷启动同步在真实数据量级下不再整次失败，并消除并发与配置边界上的崩溃/阻塞风险。所有修复必须附带能复现原缺陷的回归测试。

## 背景与证据

本 PRD 的全部问题均来自对 `internal/algorunner`（6 文件 / 2727 行）的代码审查，并对每条结论做了实测验证。相关跨层事实：

- 原生 runner（`native/src/algorithm/runner/runner.cpp`）在缺少 `--broker` 时「打印用法并 `return 0`」，而 Go 侧把任何非预期退出都当崩溃 → 退避重启 + CRASH_LOOP 熔断。
- IPC 单包负载硬上限 64KB（`internal/algoipc/protocol.go` 的 `MaxIpcPayloadSize`，与 `native/src/algorithm/ipc/ipc_protocol.hpp` 的 `MAX_IPC_PAYLOAD_SIZE` 跨语言比对）。
- 人脸特征为 512 维 float32（`internal/engine/engine.go` 与 `internal/person/service.go` 均硬校验 512），JSON 序列化后约 **12.3 bytes/float**，单条 item 约 **6.4KB**。
- `internal/person` 的 `Name` / `PersonID` **无长度校验**（`service.go` 仅校验非空；迁移 `name TEXT NOT NULL` 无长度约束），`PersonID` 可长至 64 字符（`ZHULONG_PERSON_ID_MAX_LEN`）。

## Requirements

### R1 — `StopAlgorithm` 不得在未停止目标 watcher 时写入 DISABLED

`Supervisor.StopAlgorithm` 在 `s.watchers[algoID] != w` 的情况下（说明该 ID 已被回滚/升级路径注册为新 watcher），仍然无条件把 DB 状态写成 `DISABLED`，导致「进程正在运行但 DB 说已停用」。

必须：仅当本次调用确实移除了自己的 watcher 时才写入 `DISABLED`。

### R2 — 底库分片必须按字节预算切分，而非固定条数

`CreateGallerySyncShards` 使用 `const itemsPerShard = 10`，实测 10 条真实数据已占用 64204~65373 字节（上限 65536），余量仅 0.25%~2.0%；且超限时返回 `error` 导致**整次底库同步一片都不发**（不是丢一片），冷启动后人脸底库为空。

必须：改为按累计字节预算切分，并为「单条 item 本身超出预算」提供明确可定位的错误。

### R3 — 不得在持有 `Supervisor.mu` 写锁期间执行 DB / 订阅 IO

`resolveBrokerSocksLocked` 在写锁内调用外部 `BrokerSubscriptionResolver.SyncBrokerSubscriptions`，其实现会做 2 次 DB 查询 + 逐相机路径解析。该调用点共 3 处（`StartAlgorithm` / `UpgradeAlgorithm` / `rollbackToOldLocked`），期间 `GetStatus`（RLock）全部阻塞，而 `Service.ListAlgorithms` 会对每个算法调用 `GetStatus`，导致 HTTP 列表接口出现随规模放大的延迟尖刺。

必须：把 resolver 调用移出写锁，并用「注册前二次校验」兜底解析期间产生的重复注册。

### R4 — `RecoveryOptions` 必须归一化，杜绝 `NewTicker(0)` panic

`NewSupervisor` 直接保存调用方传入的 `opts`，归一化只发生在 `NewRecoveryStateMachine` 内。而 `superviseLoop` 用 `opts.StableRunningThreshold` 直接调 `time.NewTicker`：

- `StableRunningThreshold = 0` → `panic: non-positive interval for NewTicker`（已实测为 panic，不是 error）→ goroutine panic → 整进程崩溃
- `RollbackGuardWindow = 0` → `time.NewTimer(0)` 立即触发 → `MarkStable()` → 回滚保护静默失效

`RecoveryOptions` 是导出类型（`internal/algorithm/model.go` 别名），必须防御非法值。

### R5 — `LoadDotEnv` 的解析错误不得被静默吞掉

`RunnerProcess.Start` 使用 `if loaded, err := LoadDotEnv(...); err == nil` 丢弃全部错误。文件不存在已在 `LoadDotEnv` 内部转为空 map，因此这里吞掉的都是真错误（如超长行触发 `bufio.Scanner` 的 `ErrTooLong`），会让整个 `.env` 静默失效且无任何日志。

必须：错误路径记录 Warn 日志，不改变「文件缺失即视为可选」的既有语义。

### R6 — 崩溃重启路径的 broker 列表必须在每次启动时重新解析

`superviseLoop` 的崩溃重启分支注释声明「崩溃重启路径无 ctx，复用此前解析的 brokerSocks 防 exit 5」。该假设与 `R3` 移出写锁后的语义不一致：任务集合可能已变化，而 `ctx` 实际可通过 `context.Background()` 获得。

必须：崩溃重启时重新解析 broker 列表；解析失败时回退到缓存的列表（保持不退化），并记录日志。

### R7 — 运行器进程必须以独立进程组启动并被整组终止

`exec.Command` 未设置 `SysProcAttr.Setpgid`，`SIGTERM` / `SIGKILL` 只作用于 runner 自身，其派生的孙进程会泄漏。

必须：以独立进程组启动，停止时向整个进程组发信号（含 SIGKILL 兜底），并保持既有的「SIGTERM → 超时 → SIGKILL」两段式语义不变。

## Constraints

- **不得改变跨语言线协议**：`MaxIpcPayloadSize`、IPC 头布局、消息类型编号、`GallerySyncShardPayload` 的 JSON 字段名均不得改动。分片是 Go 侧的单方行为，native 侧已按 `shard_total` / `shard_index` 聚合。
- **不得引入新的外部依赖**，不新增 `go.mod` 条目。
- **不得改变公开 API 的语义**：`CreateGallerySyncShards` 的签名与「空输入返回 1 片」行为保持；`StopAlgorithm` / `RestartAlgorithm` / `ResetAlgorithm` 对外契约保持。
- 遵守项目硬性约定：业务路径无 `panic`；不吞错误；`context.Context` 不进结构体字段；测试与源码同目录、`-race` 强制。
- 仅改动 `internal/algorunner/`（`R6` 允许触碰同一包的 `supervisor.go`；`R7` 允许在 `process.go` 内设置 `SysProcAttr`）。若发现必须改动包外文件，先停下来说明。
- **不删除**预先存在的死代码（`CreateGalleryUpsertMsg` / `CreateGalleryDeleteMsg` / `RecoveryStateMachine.Reset` 等无调用方代码仅在报告中提及，不在本任务删除范围）。

## Acceptance Criteria

- [ ] **AC1**（R1）新增并发回归测试：在升级后崩溃回滚期间并发调用 `StopAlgorithm`，断言「`GetStatus` 返回 RUNNING 时，store 中状态不得为 DISABLED」；修复前该测试失败，修复后通过。
- [ ] **AC2**（R1）`StopAlgorithm` 在确实移除自己 watcher 的正常路径上仍写入 DISABLED（由既有测试与新增测试共同覆盖）。
- [ ] **AC3**（R2）分片测试改用**真实量级特征**（512 维、约 12.3 bytes/float、真实 26 字符 ULID、中文姓名、真实图片文件名），断言每片 `<= shardBudget`；测试须在旧的 `itemsPerShard = 10` 实现下失败。
- [ ] **AC4**（R2）构造「单条 item 超出预算」的输入，断言返回错误且错误信息包含可定位标识（face_id）；断言不发生静默丢数据。
- [ ] **AC5**（R2）边界：`shard_total` 与实际片数一致、`shard_index` 从 0 连续递增、每片 items 拼接后与输入顺序/内容完全一致（不丢不重不乱序）。
- [ ] **AC6**（R3）并发测试：在 `StartAlgorithm` 解析 broker 期间并发调用 `GetStatus`，断言不因写锁阻塞而超时（用可控延迟的 resolver 制造窗口）。
- [ ] **AC7**（R3）解析期间的重复注册被兜底拒绝：两个并发 `StartAlgorithm` 对同一 ID 只有一个成功。
- [ ] **AC8**（R4）表驱动测试覆盖 `StableRunningThreshold` / `RollbackGuardWindow` / `InitialBackoff` / `MaxBackoff` / `CrashWindow` / `MaxCrashes` 为 `0` 或负数时：`NewSupervisor` 不 panic，且 `superviseLoop` 能正常启动与退出。
- [ ] **AC9**（R5）测试：`.env` 含超长行（> 64KB）时，`Start` 记录 Warn 且不 panic、不静默丢弃；文件不存在仍按可选处理（既有测试保持通过）。
- [ ] **AC10**（R6）崩溃重启后 runner 收到的 `--broker` 参数反映**最新**解析结果；解析失败时回退到缓存列表且不进入 CRASH_LOOP。
- [ ] **AC11**（R7）测试：runner 派生子进程后，`Stop` 返回时整个进程组已退出（用 `kill(-pgid, 0)` 断言不存在）。
- [ ] **AC12**（全局）`make go-check` 通过；`internal/algorunner` 的 `-race` 测试全绿。
- [ ] **AC13**（全局）不改动 `internal/algoipc` 与任何 native 文件；`git diff --stat` 仅落在 `internal/algorunner/`。

## Notes

- 现有测试 `TestCreateGallerySyncShards_MultiShardUnder64KB` 使用的是 `float32(i*1000+j)`（整数、JSON 仅 4-6 字符），10 项仅 26024 字节，比真实数据小 2.5 倍，**必然通过**任何 `itemsPerShard` 值——这是 R2 未被发现的直接原因，`AC3` 必须修掉这个假绿。
- `R4` 的生产路径目前走 `DefaultRecoveryOptions()`，所以线上未触发；但 `RecoveryOptions` 是导出类型，属于埋雷。
- `R3` 与 `R6` 是配套的：只有把 resolver 调用移出写锁，崩溃重启路径重新解析才不需要担心锁顺序问题。
- 未纳入本任务（仅在审查报告中提及，避免范围蔓延）：`dispatchGallerySync` 的 100ms 魔法延迟（native ABI 未暴露「broker 客户端已连接」信号，且 `Engine.BroadcastAIMessage` 不返回送达计数，无法在不改 native 的前提下彻底修复）、`UpgradeAlgorithm` 覆盖注册的额外加固、`RollbackGuard.pkgID` 仅用于日志、`RecoveryStateMachine.lastStableAt` 只写不读、无调用方的 `CreateGallery*Msg` 系列函数。
