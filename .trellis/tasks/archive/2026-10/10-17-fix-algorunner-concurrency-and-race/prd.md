# PRD: 修复 algorunner 并发死锁与管道竞态缺陷

## Goal

修复 `internal/algorunner` 模块在 Code Review 中暴露的全部并发安全性、互锁死锁、管道读取截断竞态以及状态一致性缺陷，精简未使用的冗余契约方法，并补齐核心升级与回滚场景的单元测试。

---

## Background & Motivation

在 `split-algorunner-supervisor` 任务中，我们将动态沙箱与自愈调度成功剥离到了 `internal/algorunner` 包。但在后续的高并发与回归审计中，发现了以下缺陷：
1. `RunnerProcess` 中 stdout/stderr 异步日志读取协程与 `cmd.Wait()` 之间缺少同步屏障（缺少 WaitGroup），在进程快速退出时会导致管道被提前关闭或测试立即读取 observer 时日志尚未落库（`TestRunnerProcessMultipleBrokersArgs` 在 `-race -count=10` 下 100% 竞态失败），且 `failureStage` 探测标记存在时序竞态；
2. `Supervisor.UpgradeAlgorithm` 在持有全局大锁 `s.mu.Lock()` 的状态下阻塞等待 `<-oldWatcher.doneCh`，而旧进程若在退出期间遭遇崩溃或自愈触发将尝试获取 `s.mu.Lock()`，导致相互死锁；同时裸写 `close(oldWatcher.stopCh)` 绕过了 `sync.Once` 防护，存在并发 panic 隐患；
3. `Supervisor.ResetAlgorithm` 在持有 `s.mu` 时执行 channel select 等待，拉长了全局排他锁的持有周期；
4. `Supervisor.RestartAlgorithm` 在启动失败时强行向 DB 刷写 `ACTIVE`，造成内存状态 (`DISABLED`) 与数据库持久化状态割裂；
5. `contracts.go` 中遗留了未使用的 `PreviousInstallDir` 和 `PreviousEntryPath` 辅助方法；
6. `UpgradeAlgorithm` 缺乏单元测试覆盖。

---

## Detailed Requirements

### 1. `RunnerProcess` 管道 Drain 与退出信号同步
- 在 `RunnerProcess` 中引入 `logWg sync.WaitGroup`，跟踪 stdout 与 stderr 两个扫描协程。
- 在 `watchExit` 中，执行 `cmd.Wait()` 之后必须等待 `logWg.Wait()` 完成，确保子进程输出流全部排尽、`failureStage` 完全固化，然后再更新状态并关闭 `doneCh`。
- 保证外部监听 `<-proc.Done()` 返回时，所有标准输出与错误输出日志已百分之百完成分发。

### 2. `Supervisor.UpgradeAlgorithm` 死锁防护与优雅停机
- 将 `oldWatcher` 的停止与等待退出逻辑移出 `s.mu` 保护范围：
  - 加锁取出并从 `s.watchers` 中删除 `oldAlgo.ID`；
  - 释放 `s.mu`；
  - 调用 `oldWatcher.stopProcess(3 * time.Second)`（使用 `sync.Once` 保护的 `requestStop`），并等待 `<-oldWatcher.doneCh`；
  - 重新获取 `s.mu` 执行新版本启动与 `RollbackGuard` 装配。
- 杜绝直接操作 `close(oldWatcher.stopCh)`。

### 3. `Supervisor.ResetAlgorithm` 锁粒度优化
- 检查 `w.status == StatusCrashLoop` 后，释放 `s.mu`，再执行 `select { case <-w.doneCh: case <-ctx.Done(): ... }`。
- 等待完成后重新获取锁完成 watcher 的安全移除，避免在等待期间堵塞其它并发查询（如 `GetStatus`）。

### 4. `Supervisor.RestartAlgorithm` 状态一致性修复
- 重启失败时（`StartAlgorithm` 返回 error），不再向 DB 强制覆写 `ACTIVE`，避免将不可用状态在持久化层伪装为激活状态。

### 5. `contracts.go` 代码清理
- 移除无任何调用方的 `PreviousInstallDir` 与 `PreviousEntryPath`。

### 6. 单测补齐与高并发防回归
- 在 `supervisor_test.go` 中新增 `TestSupervisorUpgradeAlgorithmSuccess` 与 `TestSupervisorUpgradeAlgorithmRollbackOnCrash`。
- 验证全量单元测试在 `-race -count=10` 下 100% 稳定通过。

---

## Constraints

1. **严格单向依赖**：不破坏 `algorunner` 依赖边界，禁止反向依赖 `algorithm`。
2. **零数据竞争**：全量测试必须通过 `-race` 检查。
3. **向后兼容**：不改变上层 `internal/app` 与 `internal/algorithm` 的公开对外行为。

---

## Acceptance Criteria

- [x] `RunnerProcess` 增加 `logWg` 机制，`TestRunnerProcessMultipleBrokersArgs` 在 `go test -race -count=10` 下 100% 稳定绿灯。
- [x] `UpgradeAlgorithm` 与 `ResetAlgorithm` 释放排他锁后再进行通道等待，杜绝死锁与 Panic。
- [x] `contracts.go` 冗余未引用方法清理完毕。
- [x] `supervisor_test.go` 新增针对 `UpgradeAlgorithm` 平滑升级与崩溃自动回滚的测试用例并全部通过。
- [x] `make go-check` 静态检查、vet、-race 全量通过。
