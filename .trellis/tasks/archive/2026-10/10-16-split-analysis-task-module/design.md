# Design: 拆分 analysis_task 为独立 internal/task 模块

## 1. 架构拓扑与职责边界 (Architecture Topology)

重构前：
```txt
internal/algorithm (过载)
  ├── 算法包管理 (Verifier, Store, Service, Handler)
  ├── 进程守候 (Supervisor, Process, Recovery)
  └── 分析任务管理 (AnalysisTask, TaskStore, TaskHandler, ComputeBudget)
```

重构后：
```txt
                           ┌───────────────────────────────┐
                           │   internal/app (Fx 宿主装配)  │
                           └───────┬───────────────┬───────┘
                                   │               │
                 ┌─────────────────▼───┐       ┌───▼──────────────────┐
                 │    internal/task    │       │  internal/algorithm  │
                 │                     │       │                      │
                 │ • Task 实体与 DTO   │       │ • Algorithm 实体     │
                 │ • Task Store (CRUD) │       │ • Package Verifier   │
                 │ • Task Service      │       │ • Process Supervisor │
                 │ • Task Handler      │       │ • Algorithm Handler  │
                 │ • /api/v1/tasks     │       │ • /api/v1/algorithms │
                 └─────────┬───────────┘       └───────────┬──────────┘
                           │ 依赖抽象 (只读规格)           │
                           │◄──────────────────────────────┘
                           │ (AlgorithmSpecProvider)
                           │
                           │ 触发热重启事件 (D5 契约)
                           ├──────────────────────────────►
                           │ (TaskChangeListener)
```

---

## 2. 接口设计与依赖反转 (Dependency Inversion Contracts)

为确保 `internal/task` 与 `internal/algorithm` **互不产生直接 import 依赖**，所有跨域交互采用接口反转：

### 2.1 任务向算法查询规格（`AlgorithmSpecProvider`）
在 `internal/task/service.go` 中声明：
```go
package task

// AlgorithmSpec 包含任务进行校验和算力预算所必需的算法只读元数据。
type AlgorithmSpec struct {
    ID               string
    Name             string
    ConfigSchemaJSON string
    ComputeCost      int
    Status           string
}

// AlgorithmSpecProvider 提供算法规格查询能力。
type AlgorithmSpecProvider interface {
    GetAlgorithmSpec(ctx context.Context, idOrName string) (*AlgorithmSpec, error)
}
```
`algorithm.Service` 实现一个简单适配器方法 `GetAlgorithmSpec`，在 `internal/app/app.go` 中通过 Fx 注入给 `task.NewService`。

### 2.2 任务变更通知下游（`TaskChangeListener`）
在 `internal/task/service.go` 中声明：
```go
package task

// TaskChangeListener 接收任务变更事件，用于驱动外部进程（如 Runner）热更新流订阅。
type TaskChangeListener interface {
    OnTaskChanged(ctx context.Context, algorithmID string)
}
```
`algorithm.Service.ScheduleRestart(algoID)` 天然符合该契约，在 `internal/app/app.go` 中直接作为 listener 注入。

### 2.3 宿主同步 FrameBroker 路径（`appBrokerPathResolver`）
在 `internal/app/app.go` 中，原有的 `appBrokerPathResolver` 结构体：
```go
type appBrokerPathResolver struct {
    taskStore *task.Store
    streamHub *camera.StreamHub
}
```
从 `taskStore.ListTasksByAlgorithm` 获取 enabled 的任务集合，不再访问 `algorithm.Store` 中的任务表。

---

## 3. 数据模型与 API 设计 (Models & API)

### 3.1 实体模型与表结构 (`internal/task/model.go`)
- 映射现有数据表 `analysis_tasks`：
```go
type Task struct {
    ID          string    `gorm:"primaryKey;column:id" json:"id"`
    CameraID    string    `gorm:"column:camera_id;not null;index" json:"cameraId"`
    AlgorithmID string    `gorm:"column:algorithm_id;not null;index" json:"algorithmId"`
    Enabled     bool      `gorm:"column:enabled;not null" json:"enabled"`
    SampleFPS   int       `gorm:"column:sample_fps;not null;default:5" json:"sampleFps"`
    ConfigJSON  string    `gorm:"column:config_json;not null;default:'{}'" json:"configJson"`
    CreatedAt   time.Time `gorm:"column:created_at;not null" json:"createdAt"`
    UpdatedAt   time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
}

func (Task) TableName() string {
    return "analysis_tasks"
}
```
⚠️ 约束注意：
- `Enabled` 字段不得带 `gorm:"default:true"` 标签，避免 GORM 零值陷阱。
- `SampleFPSMax = 1000`，强制调用 `ValidateSampleFPS`。

### 3.2 HTTP API 规范
标准路由组：`/api/v1/tasks`
- `GET /api/v1/tasks`：列表查询（支持 `camera_id`、`algorithm_id` Query 过滤）
- `GET /api/v1/tasks/:id`：单任务详情
- `POST /api/v1/tasks`：创建任务（状态码 201 Created）
- `PUT /api/v1/tasks/:id`：更新任务配置与帧率（补齐此前缺失的更新接口）
- `DELETE /api/v1/tasks/:id`：删除任务（状态码 200 OK，返回 `{"deleted": true}`）

兼容路由组（在 `internal/app` 或 `algorithm.Handler` 中）：
- `/api/v1/algorithms/tasks` (GET / POST)
- `/api/v1/algorithms/tasks/:id` (DELETE)
直接委托调用 `task.Handler`，保障存量前端与自动化测试平滑过渡。

---

## 4. 迁移与兼容策略 (Rollout & Compatibility)

1. **数据库无感**：表名仍为 `analysis_tasks`，迁移脚本（`000007_create_algorithm_tables.up.sql`）保持不变，生产环境部署无需运行新的 DDL。
2. **渐进式替换**：
   - 第一步：新建 `internal/task`，编写完整 Store、Service、Handler 及单测。
   - 第二步：在 `internal/algorithm` 中将直接的任务操作降级或废除，暴露 `AlgorithmSpecProvider`。
   - 第三步：在 `internal/app` 完成 Fx 编排与路由重定向。
   - 第四步：调整关联包引用（如测试代码与 broker resolver）。
3. **回滚方案**：若重构出现非预期回归，因数据库 Schema 未变更，可直接通过 Git 回滚代码，无持久化脏数据风险。
