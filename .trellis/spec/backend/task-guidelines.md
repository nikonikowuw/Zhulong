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
4. **唯一写入口**：生产的任务写入只允许 `Store.CreateTaskWithinBudget` 与 `Store.SaveTaskWithinBudget`。
   `Store.CreateTask`（无预算校验）仅服务于种子数据与测试这类“无预算语义”的场景，禁止在 Handler/Service 新路径中调用。
5. **upsert 定位与“一对多”防护**：`SaveTaskWithinBudget` 先按显式 `id`、未命中再按 `camera_id` 定位既有行；
   若目标 `camera_id` 已被另一个任务占用，返回 `ErrTaskAlreadyExists`（上层映射 409），绝不靠 `id = ? OR camera_id = ?` 一次性探测。
   并发建同一摄像头任务时预检查会失效，权威判定是 `analysis_tasks.camera_id` 唯一约束；
   冲突**不得**靠匹配 SQLite 错误字符串归因，必须在事务内做探针查询后返回 `ErrTaskAlreadyExists`。
6. **主键由 Store 在事务内决定**：`Service` 不预先分配任务 ID（否则按 camera 的 upsert 永远命中不到既有行）。
7. **区域策略缓存**：`RegionConfigCache.Reload` 是**全量重建**（每次任务变更都重扫启用中的任务），
   不做增量 diff；单条区域配置解析失败只降级为 warn 日志并跳过，不影响其余缓存项。

---

## 3.1 子项归一化契约

- **空算法列表不是「清空」而是非法配置**（`len(algorithms) == 0` → 422 `TASK_ALGORITHM_REQUIRED`）。
  清空整个通道必须走 `DELETE /api/v1/tasks/:id`。原因：`syncTaskAlgorithms` 的语义是「先按 keepIDs 剪掉不在列表里的子项，
  再 upsert 剩下的」，提交空列表会把子项剪干净、主表行原样留着，落下一行 `enabled=true, algorithms=[]` 的**空聚合根**。
  空行不占算力、不产生明细行，却会让前端按「任务行存在」渲染出算力看板与空通道卡片（即“没任务却有卡片”）。
  存量脏数据由迁移 `000019_purge_empty_analysis_tasks` 清理（幂等；down 为 no-op）。
- `normalizeAndValidateAlgorithms` 会**就地**归一化 `TaskAlgorithm`：`SampleFPS == 0` 收敛为 `DefaultSampleFPS`，
  空 `config_json` / `region_config_json` 收敛为 `{}`，并把按名字传入的 `AlgorithmID` 替换为规格 ID。
- `sample_fps` **负数不静默纠正**：直接返回 422 `INVALID_TASK_CONFIG`。上限为 `SampleFPSMax = 1000`。
- 同一任务内重复的 `algorithmId` 返回 409 `DUPLICATE_ALGORITHM`。
- `Enabled` 的"默认开启"只发生在 Handler 的请求类型层（`CreateTaskRequest.ToTask`）；**直接调用 `Service` 时必须显式设置**，
  零值 `Enabled=false` 会原样落库（这是 `gorm` 零值不回弹特性的直接后果，不要试图在 Store 里补默认值）。
- 包内不保留双份职责的构造函数：`TaskComponents` / `NewTaskComponents` 已删除，Fx 装配在 `internal/app` 内完成。

---

## 4. HTTP API 规范

### 4.1 标准端点 (`/api/v1/tasks`)

- **`GET /api/v1/tasks`**：获取所有通道任务列表（嵌套 `algorithms: [...]`）。支持 `?camera_id=xxx` 或 `?algorithm_id=xxx` 过滤；
  两者同时给出时 **`camera_id` 优先、`algorithm_id` 被忽略**（单摄像头单任务模型下没有交集语义）。
  列表始终返回非 nil 切片，空结果为 JSON `[]` 而不是 `null`。
- **`GET /api/v1/tasks/:id`**：获取指定通道任务详情（含算法配置列表）。
- **`GET /api/v1/tasks/camera/:cameraId`**：按摄像头 ID 获取通道分析任务详情。
- **`POST /api/v1/tasks`**：创建通道分析任务（201 Created，支持包含完整算法列表）。
- **`PUT /api/v1/tasks/:id`**：全量原子更新通道分析任务及其算法子项。
- **`PUT /api/v1/tasks/camera/:cameraId`**：按摄像头 ID 原子保存/更新通道任务（Upsert）。
- **`DELETE /api/v1/tasks/:id`**：删除分析任务（级联清理所有算法子项并释放算力）。

### 4.2 向下兼容端点 (`/api/v1/algorithms/tasks`)

保留原有挂载在 `/api/v1/algorithms/tasks` 下的端点，兼容接收扁平单算法字段，底层业务逻辑完全委托至 `task` 领域服务。

### 4.3 错误码与文案门禁

- 本包对外的全部机器码：`TASK_NOT_FOUND`、`TASK_ALREADY_EXISTS`、`TASK_CREATE_FAILED`、`TASK_UPDATE_FAILED`、
  `TASK_DELETE_FAILED`、`TASK_SAVE_FAILED`、`DUPLICATE_ALGORITHM`、`TASK_ALGORITHM_REQUIRED`、`INVALID_TASK_CONFIG`、
  `INVALID_REGION_CONFIG`、`INVALID_SCHEDULE_CONFIG`、`COMPUTE_BUDGET_EXCEEDED`、`ALGORITHM_NOT_FOUND`、`INVALID_REQUEST`，以及复用的
  `VALIDATION_FAILED`、`INTERNAL_ERROR`。
- 每个码都必须在 `internal/task/locale.go`（或 `httputil` 内置目录）有 en / zh-Hans / zh-Hant 三语文案；
  漏注册会让 `WriteError` 静默回退到通用 `REQUEST_FAILED` 文案。
- `internal/task/error_codes_test.go` 是这条契约的门禁：新增错误码必须同步加入 `taskErrorCodes` 表。
- `INVALID_REQUEST` 与 `internal/algorithm/locale.go` 共享同名 key，两处文案必须保持一致。

---

## 5. 前端「通道多算法」交互契约 (Frontend Channel-Centric UX)

1. **通道卡片与表格双重视角**：前端将每个摄像头呈现为「通道分析任务（Channel Task）」，同时保留扁平表格视图用于底层按算法排查。
2. **原子保存契约**：在「配置通道算法」弹窗中，用户编辑勾选状态、FPS 和配置参数后，单次调用 `PUT /tasks/camera/:cameraId` 提交，后端在事务内原子校验与持久化，彻底废除多请求 diff 脆弱逻辑。
3. **全局算力预算前置阻断**：前端基于 1,000 点总预算池实时核算 $\sum (\text{cost} \times \text{fps})$，超限时前端立即阻止提交，并在后端返回 409 `COMPUTE_BUDGET_EXCEEDED` 时友好提示。


## 绊线区域配置

`region_config_json` 在既有ROI/MASK之外支持 `tripwires`，完整字段与方向合同见 [SDK绊线配置](../native/algo-plugin-contract.md#绊线配置与区域读取sdk-120)。每算法最多8条，稳定ID和有序端点持久化；名称与方向属于配置。保存只表示配置存在，不代表算法支持或已启用统计。无需新增数据库列。Go服务继续用 `region.ParseConfig` 校验，错误复用 `INVALID_REGION_CONFIG`。

区域读取沿现有 `appBrokerPathResolver` → `TaskRegionProvider` → `TaskConfigBroadcaster.BroadcastRegions` → `MSG_TASK_REGIONS`；不改变 `config_json` 的算法自定义参数语义。任务变更按现有生命周期重启包，启动前播种Broker缓存。
