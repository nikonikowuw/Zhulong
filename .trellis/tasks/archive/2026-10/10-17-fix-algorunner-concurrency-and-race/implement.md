# Implementation Plan: 修复 algorunner 并发死锁与管道竞态缺陷

## Execution Steps

### Step 1: 修复 `RunnerProcess` 管道 Drain 与退出时序
- [x] 在 `internal/algorunner/process.go` 的 `RunnerProcess` 中添加 `logWg sync.WaitGroup`。
- [x] 在 `Start()` 中为 stdout/stderr 两个日志读取协程调用 `logWg.Add(2)` 和 `defer logWg.Done()`。
- [x] 在 `watchExit()` 中在 `cmd.Wait()` 之后调用 `p.logWg.Wait()`。
- [x] 验证：运行 `go test -race -count=10 ./internal/algorunner/... -run TestRunnerProcessMultipleBrokersArgs` 确认 100% 通过。

### Step 2: 修复 `Supervisor` 死锁与通道等待锁占用
- [x] 修改 `internal/algorunner/supervisor.go` 中的 `UpgradeAlgorithm`，释放锁后再执行 `stopProcess` 与 `<-oldWatcher.doneCh`，移除原危险的裸 `close(oldWatcher.stopCh)`。
- [x] 修改 `internal/algorunner/supervisor.go` 中的 `ResetAlgorithm`，释放 `s.mu` 后等待 `<-w.doneCh`。
- [x] 修复 `RestartAlgorithm` 在启动失败时向 DB 回写 `ACTIVE` 的逻辑。
- [x] 验证：运行 `go test -race ./internal/algorunner/...`。

### Step 3: 清理 `contracts.go` 冗余代码
- [x] 删除 `internal/algorunner/contracts.go` 中的 `PreviousInstallDir` 和 `PreviousEntryPath`。
- [x] 验证编译与现有包测试：`go test ./internal/algorunner/... ./internal/algorithm/...`。

### Step 4: 补齐 `UpgradeAlgorithm` 单元测试
- [x] 在 `internal/algorunner/supervisor_test.go` 中编写 `TestSupervisorUpgradeAlgorithmSuccess` 和 `TestSupervisorUpgradeAlgorithmRollbackOnCrash`。
- [x] 验证：`go test -race -v ./internal/algorunner/... -run TestSupervisorUpgradeAlgorithm`。

### Step 5: 全局质量门禁与 Spec 检查
- [x] 运行全量 `go test -race -count=5 ./internal/algorunner/...` 确保无任何 flakiness。
- [x] 运行 `make go-check` 验证 Native C++、Go vet、lint 与全量集成测试通过。
