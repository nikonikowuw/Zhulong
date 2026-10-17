# 实施计划：修复原生引擎 CGO 桥接并发安全与生命周期缺陷

## 任务拆解与执行顺序

### Step 1: 消除 `Stream.Close` 数据竞争
- [x] 修改 `internal/engine/stream.go`：在 `Stream.Close()` 的 `s.mu.Lock()` 临界区内原子读取并重置 `s.activeSub` 与 `s.activeAISub`。
- [x] 编写并发回归测试：在 `internal/engine/stream_race_test.go` 中复现并验证高并发 `Close()` 与 `SubscribeAIResults` 无 race。
- 验证命令：`python3 native/scripts/build.py go test -race ./internal/engine/ -run TestStreamCloseRace`

### Step 2: 引入 `handleMu` 保护 CGO 句柄生命周期
- [x] 修改 `internal/engine/engine.go`：
  - 在 `Engine` 结构体新增 `handleMu sync.RWMutex` 及 `pendingTokens []func()`。
  - 在 `Acquire`, `getStreamStatus`, `subscribe`, `unsubscribe`, `subscribeAIResults`, `unsubscribeAIResults`, `releaseStream`, `getBrokerPath`, `setPublishRate`, `BroadcastAIMessage` 中加读锁保护 CGO 执行生命周期。
  - 在 `Close()` 中停机完成后加写锁销毁 `handle`。
- [x] 编写并发生命周期测试：在 `internal/engine/engine_test.go` 中增加高并发 `Close()` 与 CGO 状态查询/广播调用的竞态测试。
- 验证命令：`python3 native/scripts/build.py go test -race ./internal/engine/ -run TestEngineConcurrentLifecycle`

### Step 3: `cgo.Handle` 延迟注销机制
- [x] 修改 `internal/engine/engine.go` 中的 `unsubscribe` 与 `unsubscribeAIResults`：
  - 遇到 `Zhulong_ERR_NOT_RUNNING` 时将 `deleteToken` 记录至 `e.pendingTokens`。
  - 在 `stopInternal()` 停机完成后（`C.Zhulong_engine_stop` 已返回，所有 worker join 完毕），排空执行 `e.pendingTokens`。
- [x] 编写测试覆盖退订在 Native 停机时期的行为。
- 验证命令：`python3 native/scripts/build.py go test -race ./internal/engine/ -run TestUnsubscribeWhenStopping`

### Step 4: `AIResultSubscription` 运行态流失败感知
- [x] 修改 `internal/engine/ai_subscription.go` 与 `internal/engine/stream.go`：
  - 为 `AIResultSubscription` 增加 `runControlLoop(ctx)`，轮询 `stream.Status(ctx)`，并在 `StreamFailed` 时及时中断订阅并抛出底层错误。
  - 在 `Stream.SubscribeAIResults` 中启动 `runControlLoop`。
- [x] 编写测试：模拟物理流进入 `StreamFailed`，验证 `AIResultSubscription.Next` 及时返回错误而非永驻阻塞。
- 验证命令：`python3 native/scripts/build.py go test -race ./internal/engine/ -run TestAIResultSubscription_StreamFailed`

### Step 5: 输入防御与协议契约同步
- [x] 修改 `internal/engine/engine.go`：
  - 定义 `const MaxFaceImageDimension = 4096`，在 `ExtractFaceFeature` 中校验宽高，超限直接返回 `ErrInvalidArgument`。
  - `BroadcastAIMessage` 引入 `internal/algoipc`，使用 `algoipc.MaxIpcPayloadSize` 进行负载长度上限校验。
- [x] 在 `internal/engine/validation_test.go` 中补充 `ExtractFaceFeature` 超大尺寸拦截测试与所有 `Err*` 哨兵错误的 `errors.Is` 断言。
- 验证命令：`python3 native/scripts/build.py go test -race ./internal/engine/ -run "TestExtractFaceFeature|TestErrorSanitization"`

### Step 6: 全局质量检验与回归
- [x] 运行 `python3 native/scripts/build.py go test -race ./internal/engine/...`。
- [x] 运行 `make go-check` 确认全局契约完整。
