# 视频分析任务管理开发规范 (Analysis Task Specification)

> Zhulong 视觉计算平台分析任务管理规范：领域模型、跨域契约（接口反转）、算力预算控制与 HTTP RESTful API。

---

## 1. 领域定位与职责划分

- **核心定位**：`internal/task` 是负责视频流分析任务调度的独立领域包，负责 Camera 与 Algorithm 之间的动态业务绑定。
- **通道聚合根自治**：以视频通道（Camera）为主体建立 `Task` 聚合根，下辖若干 `TaskAlgorithm` 算法子项，实现单通道多算法统一调度。
- **与算法包资产解耦**：
  - `internal/algorithm`：负责算法包制品资产（静态文件、解压验包、版本管理）与外部 Runner 子进程（Supervisor）。
  - `internal/task`：负责分析任务生命周期（CRUD、配置校验、采样帧率、算力预算分配）。

---

## 2. 跨域依赖反转契约 (Dependency Inversion)

为防止 `task` 与 `algorithm` 发生循环导入，领域间交互完全基于接口反转：

### 2.1 算法规格查询接口 (`AlgorithmSpecProvider`)
```go
type AlgorithmSpec struct {
    ID               string
    Name             string
    ConfigSchemaJSON string
    ComputeCost      int
    Status           string
}

type AlgorithmSpecProvider interface {
    GetAlgorithmSpec(ctx context.Context, idOrName string) (*AlgorithmSpec, error)
}
```
- 由 `task.Service` 声明，`algorithm.Service` 实现并在 `internal/app` 装配时注入。

### 2.2 任务变更通知钩子 (`TaskChangeListener`)
```go
type TaskChangeListener interface {
    OnTaskChanged(ctx context.Context, algorithmID string)
}
```
- 任务创建、更新或删除成功后触发，向所有增删改的算法 ID 广播，由 `algorithm.Service.ScheduleAlgorithmRestart` 接收以执行短窗口防抖热重启，更新对应算法子进程的 `--broker` 订阅集（D5 契约）。

---

## 3. 持久化与算力预算控制 (Store & Compute Budget)

1. **主子表结构**：
   - 主表 `analysis_tasks`：聚合根，强约束 `UNIQUE(camera_id)`，拥有 `id`, `camera_id`, `enabled`（通道总开关）, `name`, `created_at`, `updated_at`。
   - 子表 `task_algorithms`：配置明细，拥有 `id`, `task_id`（外键级联）, `algorithm_id`, `enabled`（算法子开关）, `sample_fps`, `config_json`, `region_config_json`, `created_at`, `updated_at`，强约束 `UNIQUE(task_id, algorithm_id)`。
2. **GORM 零值防回弹**：`Task.Enabled` 与 `TaskAlgorithm.Enabled` 严禁标注 `gorm:"default:true"`，确保显式保存 `enabled=false` 时不会被 GORM 零值机制静默忽略。
3. **算力预算池**：
   - 全平台总算力预算池基准为 **1,000 点**（`MaxComputePoints = 1000`）；
   - 任务消耗计算公式：
     $$\text{TaskPoints} = \begin{cases} 
     0, & \text{if } \text{Task.Enabled} = \text{false} \\ 
     \sum_{ta \in \text{Algorithms}, ta.\text{Enabled}} (ta.\text{SampleFPS} \times \text{algo.ComputeCost}), & \text{if } \text{Task.Enabled} = \text{true}
     \end{cases}$$
   - 算力检查在保存通道任务的单一数据库事务中与现有其他 enabled 通道任务累加，超限直接阻断并返回 409 `COMPUTE_BUDGET_EXCEEDED`。

---

## 4. HTTP API 规范

### 4.1 标准端点 (`/api/v1/tasks`)

- **`GET /api/v1/tasks`**：获取所有通道任务列表（嵌套 `algorithms: [...]`）。支持 `?camera_id=xxx` 或 `?algorithm_id=xxx` 过滤。
- **`GET /api/v1/tasks/:id`**：获取指定通道任务详情（含算法配置列表）。
- **`GET /api/v1/tasks/camera/:cameraId`**：按摄像头 ID 获取通道分析任务详情。
- **`POST /api/v1/tasks`**：创建通道分析任务（201 Created，支持包含完整算法列表）。
- **`PUT /api/v1/tasks/:id`**：全量原子更新通道分析任务及其算法子项。
- **`PUT /api/v1/tasks/camera/:cameraId`**：按摄像头 ID 原子保存/更新通道任务（Upsert）。
- **`DELETE /api/v1/tasks/:id`**：删除分析任务（级联清理所有算法子项并释放算力）。

### 4.2 向下兼容端点 (`/api/v1/algorithms/tasks`)

保留原有挂载在 `/api/v1/algorithms/tasks` 下的端点，兼容接收扁平单算法字段，底层业务逻辑完全委托至 `task` 领域服务。

---

## 5. 前端「通道多算法」交互契约 (Frontend Channel-Centric UX)

1. **通道卡片与表格双重视角**：前端将每个摄像头呈现为「通道分析任务（Channel Task）」，同时保留扁平表格视图用于底层按算法排查。
2. **原子保存契约**：在「配置通道算法」弹窗中，用户编辑勾选状态、FPS 和配置参数后，单次调用 `PUT /tasks/camera/:cameraId` 提交，后端在事务内原子校验与持久化，彻底废除多请求 diff 脆弱逻辑。
3. **全局算力预算前置阻断**：前端基于 1,000 点总预算池实时核算 $\sum (\text{cost} \times \text{fps})$，超限时前端立即阻止提交，并在后端返回 409 `COMPUTE_BUDGET_EXCEEDED` 时友好提示。
