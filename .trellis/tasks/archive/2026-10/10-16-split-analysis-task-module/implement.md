# Execution Plan: 拆分 analysis_task 为独立 internal/task 模块

## Ordered Checklist

- [x] **Step 1: 创建 `internal/task` 领域包基础定义**
  - 创建 `internal/task/model.go`：定义 `Task` 实体、`CreateTaskRequest`、`UpdateTaskRequest`、`ValidateSampleFPS`、`SampleFPSMax` 以及审计动作常量。
  - 创建 `internal/task/locale.go`：注册任务相关的机器错误码与本地化多语言消息。
  - 创建 `internal/task/config_schema.go`：封装任务配置基于 JSON Schema 的校验逻辑（迁移原 `validateTaskConfig`）。
  - *验证*：`go test -race ./internal/task` 编译通过。

- [x] **Step 2: 实现 `internal/task/store.go` 与存储测试**
  - 实现基于 `database.DBProvider` 的 `Store`：
    - `CreateTask`, `CreateTaskWithinBudget`, `GetTask`, `GetTaskByCameraAndAlgorithm`, `ListTasks`, `ListTasksByCamera`, `ListTasksByAlgorithm`, `UpdateTask`, `DeleteTask`, `DeleteTasksByCamera`, `DeleteTasksByAlgorithm`。
  - 编写 `internal/task/store_test.go`：覆盖 CRUD、算力预算上限校验、GORM `enabled=false` 零值防回弹回归测试。
  - *验证*：`go test -race -v ./internal/task -run TestStore` 通过。

- [x] **Step 3: 实现 `internal/task/service.go` 与服务测试**
  - 定义 `AlgorithmSpecProvider` 与 `TaskChangeListener` 接口。
  - 实现 `Service`：`CreateTask`、`UpdateTask`、`DeleteTask`、`GetTask`、`ListTasks`、`DeleteTasksByCamera`、`DeleteTasksByAlgorithm`。
  - 编写 `internal/task/service_test.go`：Mock 算法规格提供者与监听器，验证配置校验、预算超限拒绝、以及变更后通知机制。
  - *验证*：`go test -race -v ./internal/task -run TestService` 通过。

- [x] **Step 4: 实现 `internal/task/handler.go` 与控制面测试**
  - 实现 `Handler`：
    - `ListTasks` (`GET /api/v1/tasks`)
    - `GetTask` (`GET /api/v1/tasks/:id`)
    - `CreateTask` (`POST /api/v1/tasks`)
    - `UpdateTask` (`PUT /api/v1/tasks/:id`)
    - `DeleteTask` (`DELETE /api/v1/tasks/:id`)
  - 接入 `audithttp.Tag` 与 `httputil.WriteError`。
  - 编写 `internal/task/handler_test.go`：测试 HTTP 契约、状态码（200, 201, 400, 404, 409, 422）与响应信封。
  - *验证*：`go test -race -v ./internal/task -run TestHandler` 通过。

- [x] **Step 5: 封装 `internal/task/module.go` (Uber Fx)**
  - 定义 `TaskComponents`，提供构造器 `NewTaskComponents` 与路由注册器 `ProtectedRoutes`。
  - *验证*：模块结构编译无误。

- [x] **Step 6: 解耦 `internal/algorithm` 中的任务逻辑**
  - 在 `internal/algorithm/service.go` 中实现 `GetAlgorithmSpec`，对接 `task.AlgorithmSpecProvider`。
  - 暴露 `ScheduleAlgorithmRestart`（可由 `TaskChangeListener` 调用）。
  - 清理 `algorithm.Store`、`algorithm.Service`、`algorithm.Handler` 中已被剥离的任务管理代码。
  - 在 `algorithm.Handler` 中保留对 `/algorithms/tasks` 的兼容路由转发至 `task.Handler`。
  - *验证*：`python3 native/scripts/build.py go test -race ./internal/algorithm` 通过。

- [x] **Step 7: 在 `internal/app` 完成整体装配**
  - 在 `internal/app/app.go` 引入 `task.NewTaskComponents` 并注入 Fx。
  - 将 `appBrokerPathResolver` 改为依赖 `task.Store`。
  - 调整生命周期与启动时依赖，确保优雅启动与退出。
  - 更新 `internal/app` 相关测试（`broker_resolver_test.go` 等）。
  - *验证*：`python3 native/scripts/build.py go test -race ./internal/app` 通过。

- [x] **Step 8: 全量回归与规范检查**
  - 运行全量后端测试：`python3 native/scripts/build.py go test -race ./internal/...`。
  - 运行全流程静态检查：`make check`。
