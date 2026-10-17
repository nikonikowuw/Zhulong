# Implementation Plan: 拆分子进程看护与容灾自愈为独立 algorunner 模块

## Execution Steps

### Step 1: 创建 `internal/algorunner` 基础契约与迁移 `recovery.go`
- [x] 创建 `internal/algorunner/contracts.go`，定义 `PackageMeta`、`LifecycleStore`、`BrokerSubscriptionResolver` 等接口契约。
- [x] 迁移 `internal/algorithm/recovery.go` 及 `recovery_test.go` 至 `internal/algorunner/`。
- [x] 验证：`go test -race ./internal/algorunner/...`。

### Step 2: 迁移 `process.go` 与 `supervisor.go` 至 `internal/algorunner`
- [x] 迁移 `process.go` 及 `process_test.go` 至 `internal/algorunner/`，适配引用 `algoipc` 与 `aitypes`。
- [x] 迁移 `supervisor.go` 及 `supervisor_test.go` 至 `internal/algorunner/`，将其存储依赖替换为 `LifecycleStore` 抽象接口。
- [x] 验证：`go test -race ./internal/algorunner/...`。

### Step 3: 更新 `internal/algorithm`（瘦身收敛）
- [x] 在 `internal/algorithm/store.go` 中实现 `algorunner.LifecycleStore` 接口。
- [x] 从 `internal/algorithm` 中删除原 `recovery.go`、`process.go`、`supervisor.go` 及其对应测试文件。
- [x] 更新 `internal/algorithm/service.go` 与 `service_test.go`，使用 `*algorunner.Supervisor` 或接口。
- [x] 验证：`python3 native/scripts/build.py go test -race ./internal/algorithm/... ./internal/algorunner/...`。

### Step 4: 更新 `internal/app` 依赖注入与全局适配
- [x] 修改 `internal/app/app.go`，将 `algorunner.NewSupervisor` 注册进 Uber Fx，适配 `GalleryBroadcaster`。
- [x] 修复受影响的集成测试（如 `app/broker_resolver_test.go`）。
- [x] 运行全量单元与集成测试：`python3 native/scripts/build.py go test -race ./cmd/... ./internal/...`。

### Step 5: 全局质量门禁与 Spec 归档
- [x] 运行 `make go-check` 验证 Native C++ 编译、gofmt、go vet、-race 单元与集成测试。
- [x] 运行 `make native-test` 验证 20/20 CTest 通过。
- [x] 更新 `.trellis/spec/backend/directory-structure.md` 固化 `internal/algorunner` 模块边界。
