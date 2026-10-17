# 视频分析任务管理开发规范 (Analysis Task Specification)

> Zhulong 视觉计算平台分析任务管理规范：领域模型、跨域契约（接口反转）、算力预算控制与 HTTP RESTful API。

---

## 1. 领域定位与职责划分

- **核心定位**：`internal/task` 是负责视频流分析任务调度的独立领域包，负责 Camera 与 Algorithm 之间的动态业务绑定。
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
- 任务创建、更新或删除成功后触发，由 `algorithm.Service.ScheduleAlgorithmRestart` 接收以执行短窗口防抖热重启，更新对应算法子进程的 `--broker` 订阅集（D5 契约）。

---

## 3. 持久化与算力预算控制 (Store & Compute Budget)

1. **数据表与实体**：映射 SQLite `analysis_tasks` 表，实体主键统一采用 `idutil.New()` 生成 ULID。
2. **唯一约束**：数据表强约束 `UNIQUE(camera_id, algorithm_id)`，同一摄像头同一算法只允许存在一个分析任务。
3. **GORM 零值防回弹**：`Task.Enabled` 严禁标注 `gorm:"default:true"`，确保显式保存 `enabled=false` 时不会被 GORM 零值机制静默忽略。
4. **算力预算池**：
   - 全平台总算力预算池基准为 **1,000 点**（`MaxComputePoints = 1000`）；
   - 任务消耗计算公式：$\text{Task Points} = \text{algorithm.compute\_cost} \times \text{task.sample\_fps}$；
   - 算力检查必须在数据库事务中与现有 enabled 任务累加，超限直接阻断并返回 409 `COMPUTE_BUDGET_EXCEEDED`。

---

## 4. HTTP API 规范

### 4.1 标准端点 (`/api/v1/tasks`)

- **`GET /api/v1/tasks`**：支持 `?camera_id=xxx` 或 `?algorithm_id=xxx` 过滤。
- **`GET /api/v1/tasks/:id`**：获取指定任务详情。
- **`POST /api/v1/tasks`**：创建分析任务（201 Created）。
- **`PUT /api/v1/tasks/:id`**：更新任务采样帧率、启用状态与配置 JSON。
- **`DELETE /api/v1/tasks/:id`**：删除分析任务。

### 4.2 向下兼容端点 (`/api/v1/algorithms/tasks`)

为平滑兼容存量客户端与外部自动化测试，保留原有挂载在 `/api/v1/algorithms/tasks` 下的端点，其底层业务逻辑完全委托至 `task` 领域服务。

---

## 5. 前端「通道多算法聚合」契约 (Frontend Channel-Centric UX)

为了对齐旷视、英特灵达等成熟商业边缘计算产品的操作心智，前端体验层做了一层业务聚合抽象：
1. **聚合呈现**：前端将同一个 `cameraId` 下的所有分析任务，在卡片视图中聚合呈现为「通道分析任务（Channel Task）」，同时保留扁平表格视图方便底层指标排查。
2. **多算法批量差量同步**：在「配置通道算法」弹窗中，用户可一次性为同一摄像头勾选多个算法并调整各自的 FPS；前端在保存时自动计算与现有任务的差量（POST 新增、PUT 修改、DELETE 取消勾选），实现原子批量调度。
3. **全局算力预算前置阻断**：前端基于 1,000 点总预算池实时核算 $\sum (\text{cost} \times \text{fps})$，超限时前端立即阻止提交，并在后端返回 409 `COMPUTE_BUDGET_EXCEEDED` 时优雅回弹复原。

