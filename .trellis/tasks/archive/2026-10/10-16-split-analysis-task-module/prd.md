# PRD: 拆分 analysis_task 为独立 internal/task 模块

## Goal

将分析任务（`AnalysisTask`）从 `internal/algorithm` 领域中彻底解耦出来，建立独立的 `internal/task` 顶级领域包，清晰划分「算法包资产管理」与「分析任务调度管理」的职责边界，提供符合 RESTful 标准的任务管理 API，并通过接口反转实现干净的跨域联动契约。

---

## Background & Motivation

在当前的边缘视觉计算系统中：
1. **职责过载**：`internal/algorithm` 同时承载了算法包验签解压、版本管理、沙箱进程看护（Supervisor）以及分析任务的 CRUD 和算力配额计算，导致领域内聚度低。
2. **抽象错位**：算法包（`Algorithm`）属于静态资产/制品域，而任务（`Task`）属于动态业务关联域（Camera 与 Algorithm 的 N:M 绑定），生命周期与变更频率截然不同。
3. **接口不规范**：任务 HTTP 端点原挂在 `/api/v1/algorithms/tasks` 下，且缺少 `PUT /tasks/:id` 任务更新端点。
4. **架构演进**：将 Task 独立为一等公民，为后续支持分析流流水线编排（Pipeline）、定时布防计划和告警策略联动打下基础。

---

## Requirements

### 1. 独立领域包 `internal/task`
- **模型层 (`model.go`)**：
  - 定义 `Task`（映射 SQLite `analysis_tasks` 表，兼容已有表结构）。
  - 保留 `UNIQUE(camera_id, algorithm_id)` 约束。
  - `SampleFPSMax = 1000` 与 `ValidateSampleFPS` 帧率安全校验。
  - 任务 DTO：`CreateTaskRequest`, `UpdateTaskRequest`（支持 camelCase 与 snake_case 兼容解析）。
- **持久化层 (`store.go`)**：
  - 基于 `database.DBProvider` 实现独立 `Store`。
  - 支持 `CreateTask`, `CreateTaskWithinBudget`, `GetTask`, `GetTaskByCameraAndAlgorithm`, `ListTasks`, `ListTasksByCamera`, `ListTasksByAlgorithm`, `UpdateTask`, `DeleteTask`, `DeleteTasksByCamera`, `DeleteTasksByAlgorithm`。
  - 算力预算检查：启用任务的算法总算力不得超出 `MaxComputePoints` (1000 点)。
- **业务服务层 (`service.go`)**：
  - 依赖抽象接口 `AlgorithmSpecProvider` 获取算法的 Schema 和 ComputeCost，实现防循环依赖。
  - 校验任务配置是否符合算法的 `config_schema_json`。
  - 任务增删改成功后，触发 `TaskChangeListener` 回调通知下游（如触发算法子进程的 Broker 订阅重解析）。
  - 全面采用 `apperr.Error`，统一机器码与错误分类（`KindNotFound`, `KindInvalid`, `KindConflict` 等）。
- **HTTP 控制面 (`handler.go`)**：
  - 注册标准路由组 `/api/v1/tasks`：
    - `GET /api/v1/tasks` (支持 `camera_id`, `algorithm_id` 过滤)
    - `GET /api/v1/tasks/:id`
    - `POST /api/v1/tasks` (创建任务)
    - `PUT /api/v1/tasks/:id` (完整更新任务配置与帧率)
    - `DELETE /api/v1/tasks/:id` (删除任务)
  - 审计日志打点：`task.create`, `task.update`, `task.delete`。
  - 统一使用 `httputil.Success` / `httputil.SuccessWithStatus` 与 `httputil.WriteError`。
- **国际化与错误字典 (`locale.go`)**：
  - 注册 `TASK_NOT_FOUND`, `TASK_ALREADY_EXISTS`, `INVALID_TASK_CONFIG`, `COMPUTE_BUDGET_EXCEEDED` 等 i18n 错误信息。
- **模块依赖注入 (`module.go`)**：
  - 提供 `TaskComponents` 与 Fx 构造器 `NewTaskComponents`。

### 2. 重构 `internal/algorithm`
- 从 `internal/algorithm` 中移除 `AnalysisTask`、任务 Store 方法以及任务 HTTP 端点。
- 保留类型别名或过渡定义以保持向后兼容（若有），将任务控制逻辑完全交由 `internal/task`。
- `algorithm.Service` 提供轻量适配器实现 `task.AlgorithmSpecProvider`。
- `algorithm.Supervisor` 保持纯粹的进程管理，不再直接依赖 `algorithm.Store` 中的任务方法。

### 3. 在 `internal/app` 完成装配与契约联动
- 将 `TaskComponents` 接入 Uber Fx 生命周期与路由组。
- `appBrokerPathResolver` 改为从 `task.Store` 获取启用的任务列表，解析摄像头流的 FrameBroker socket 路径并配置发布帧率。
- 建立 `task.TaskChangeListener` 联动契约：当任务创建、更新、删除时，通知 `algorithm.Service` 执行防抖热重启（D5 契约）。
- 为保证旧客户端兼容性，在 `/api/v1/algorithms/tasks` 挂载兼容路由，转发或委托给 `task.Handler`。

---

## Constraints

1. **数据库零破坏 (Zero Schema Migration)**：直接复用已有的 `analysis_tasks` 表，不修改字段定义，不破坏已持久化数据。
2. **外部兼容性 (Backward Compatibility)**：保留现有 `/api/v1/algorithms/tasks` 路由兼容，同时新增标准 `/api/v1/tasks` 路由。
3. **零跨包循环依赖 (Zero Circular Dependencies)**：`task` 与 `algorithm` 之间严禁直接相互 import，必须通过接口依赖反转在 `app` 层组装。
4. **并发与资源安全**：保持算力点数上限检查（1000 点）、任务增删改防抖动重启、数据竞争检测通过。

---

## Acceptance Criteria

- [x] 新建 `internal/task` 包，包含 `model.go`, `store.go`, `service.go`, `handler.go`, `locale.go`, `module.go` 及对应单元测试。
- [x] 完整支持 `/api/v1/tasks` 的 CRUD（包括之前遗漏的 `PUT /tasks/:id`）。
- [x] `internal/algorithm` 彻底剥离任务管理代码，职责单一化为算法包与进程管理。
- [x] `internal/app` 成功注入 `task.NewTaskComponents`，并正确联调 `appBrokerPathResolver` 与防抖重启契约。
- [x] 旧端点 `/api/v1/algorithms/tasks` 请求仍能正常工作并保持兼容。
- [x] `python3 native/scripts/build.py go test -race ./internal/...` 全量通过，无 race condition。
- [x] `make check` 通过，前后端构建及测试无破坏性影响。
