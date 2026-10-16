# Implementation Plan: internal/algorithm 模块瘦身与能力解耦

## Execution Steps

### Step 1: 创建下沉基础包 `internal/aitypes` 与 `internal/algoipc`
- [x] 创建 `internal/aitypes/model.go`，迁入 `InferenceResult`、`DetectionItem`、`ClassificationItem`、`PersonItem`、`FaceSubItem`、`FaceCandidate`、`ResultType`。
- [x] 编写 `internal/aitypes/model_test.go`，测试结构体 JSON 序列化/反序列化。
- [x] 创建 `internal/algoipc/protocol.go`，迁入 `IpcMagic`、`IpcHeader`、`EncodeIpcMessage`、`DecodeIpcHeader`、`MaxIpcPayloadSize` 等。
- [x] 编写 `internal/algoipc/protocol_test.go` 与 `ipc_contract_test.go`。
- [x] 验证：`go test -race ./internal/aitypes/... ./internal/algoipc/...`。

### Step 2: 解除 `camera` 与 `record` 对 `algorithm` 的依赖
- [x] 修改 `internal/camera/stream_hub.go`，使用 `aitypes.InferenceResult`。
- [x] 修改 `internal/record/service.go` 与 `responses.go`，使用 `aitypes`。
- [x] 修改 `internal/algorithm/result.go`，使用类型别名指向 `aitypes`（保持内部与过渡代码兼容）。
- [x] 验证：`go test -race ./internal/camera/... ./internal/record/...`。

### Step 3: 清理 `internal/algorithm` 中残留的 Task 代码并迁移兼容路由
- [x] 在 `internal/task/handler.go` 中增加兼容路由 `/api/v1/algorithms/tasks`，处理向下兼容逻辑。
- [x] 从 `internal/algorithm/handler.go` 移除 `ListTasks`, `CreateTask`, `DeleteTask` 及对应的路由挂载。
- [x] 删除 `internal/algorithm/requests.go`。
- [x] 从 `internal/algorithm/service.go` 移除 `CreateTask`, `UpdateTask`, `DeleteTask`, `ListTasks`，保留 `OnTaskChanged` 与 `GetAlgorithmSpec`。
- [x] 从 `internal/algorithm/store.go` 移除所有 Task 代理 CRUD 方法。
- [x] 在 `internal/task/handler.go` 中挂载兼容路由，确保统一走 Task 业务校验与审计。
- [x] 验证：`python3 native/scripts/build.py go test -race ./internal/task/... ./internal/algorithm/... ./internal/app/...`。

### Step 4: 将人脸底库广播逻辑解耦与通信泛化
- [x] 下沉二进制 IPC 协议至 `internal/algoipc`，解除硬编码。
- [x] `internal/algorithm` 中 `AIMessageBroadcaster` 与 `GalleryBroadcaster` 接口化，`person` 模块仅依赖自身抽象接口 `GalleryNotifier`，实现领域解耦。
- [x] 更新 `internal/app/app.go` 中的依赖注入装配。
- [x] 验证人脸底库全量与增量同步相关测试：`go test -race ./internal/person/... ./internal/algorithm/...`。

### Step 5: 全局质量检查与文档对齐
- [x] 运行 `python3 native/scripts/build.py go test -race ./internal/...` 确保全量测试绿灯。
- [x] 运行 `make go-check` 验证 Native C++ 编译、gofmt、go vet、-race 单元与集成测试。
- [x] 运行 `make native-test` 验证 20/20 CTest 全量通过。
- [x] 更新 `.trellis/spec/backend/directory-structure.md` 与 `algorithm-guidelines.md`，固化新的模块边界规范。
