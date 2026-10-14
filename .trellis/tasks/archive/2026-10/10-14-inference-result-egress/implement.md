# 实施计划：推理结果跨进程出口 (Inference Result Egress)

> 任务：`10-14-inference-result-egress`
> 父任务：`10-14-face-recognition-e2e`

---

## 检查清单

### 步骤 1：IPC 协议扩展与跨语言常量护栏
- [x] 在 `native/src/algorithm/ipc/ipc_protocol.hpp` 中增加 `MSG_RESULT = 7`。
- [x] 在 `internal/algorithm/ipc_contract_test.go` 中增加对 `MSG_RESULT == 7` 的跨语言单测校验。
- **验证**：`go test -race ./internal/algorithm/... -run TestIpcContract`

### 步骤 2：Native Runner 结果序列化与状态机过滤
- [x] 在 `native/src/algorithm/runner/runner.cpp` 中实现结果序列化辅助函数（处理 Detection、Classification 与 PersonStructure）。
- [x] 在 `runner.cpp` 消息循环中，增加目标状态机过滤（空帧静默过滤 + 单帧退出信标）。
- [x] 在 `api.process()` 返回后、`api.result_release()` 之前序列化并发送 `MSG_RESULT`。
- **验证**：编译 runner，并通过 Mock 插件集成测试验证发送内容与内存安全。

### 步骤 3：FrameBroker 与 ZhulongEngine 内部路由
- [x] 在 `native/src/algorithm/ipc/frame_broker.hpp` 与 `frame_broker.cpp` 中处理 `MSG_RESULT`，并提供 `set_on_result` 回调注册。
- [x] 在 `native/src/pipeline/engine.hpp` 与 `engine.cpp` 中，将 FrameBroker 的结果事件与引擎全局 AI 订阅分发器对接。
- **验证**：CTest 单元测试验证 FrameBroker 接收与分发 `MSG_RESULT`。

### 步骤 4：Native C ABI 扩展与 CGO 桥接
- [x] 在 `native/include/Zhulong/engine.h` 增加 `Zhulong_ai_result_callback`、`Zhulong_stream_subscribe_ai_results` 和 `Zhulong_stream_unsubscribe_ai_results`。
- [x] 在 `native/src/abi/engine.cpp` 实现上述导出函数。
- [x] 在 `internal/engine/cgo_bridge.go` 增加 export 回调 `zhulongGoAIResultCallback`，并保证使用 `cgo.Handle` 且无 Go 指针穿越。
- **验证**：`make native-test` 全部通过。

### 步骤 5：Go 侧 AIResultSubscription 与丢最旧背压
- [x] 在 `internal/engine/` 中实现 `AIResultSubscription`。
- [x] 采用固定容量环形队列（默认 32），实现 Drop-Oldest 丢最旧背压，并维护 `DroppedResults` 计数。
- [x] 编写单测验证：满缓冲慢消费时不阻塞、丢弃最旧数据、计数准确。
- **验证**：`go test -race -v ./internal/engine -run TestAIResultSubscription`

### 步骤 6：Go 宿主消费接线与消除死代码
- [x] 在 `internal/algorithm/` 中实现 `ResultConsumer` 或解析逻辑，将 Native 传来的 JSON 解码为 `algorithm.InferenceResult`。
- [x] 接入 `NewDetectionResult`、`NewClassificationResult`、`NewPersonStructureResult`，使其成为活跃调用方，解除未使用的死代码状态。
- [x] 在 `internal/camera/stream_hub.go` 或 `internal/app` 中接通流生命周期。
- **验证**：`go test -race ./internal/...`

### 步骤 7：全面回归验证与契约规范同步
- [x] 运行 ASan 测试检查无 use-after-free：`python3 native/scripts/build.py --sanitizer address test`
- [x] 运行完整宿主测试：`TMPDIR=/home/niko/tmp make go-check`
- [x] 更新 `.trellis/spec/native/algo-plugin-contract.md` 文档。
