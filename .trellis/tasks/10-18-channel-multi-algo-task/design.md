# Design: 通道多算法分析任务模型架构设计 (Channel Multi-Algorithm Task Architecture)

## 1. 架构目标与设计原则

将分析任务从历史上的 `(camera_id, algorithm_id)` 一对一绑定，重构为以视频通道（Camera）为主体的聚合根模型：
1. **聚合根自治（Aggregate Root）**：以通道（Camera）为中心构建 `Task` 聚合根，下辖若干 `TaskAlgorithm` 算法子项。通道级主开关（Master Switch）与算法级子开关分层控制。
2. **原子事务持久化与算力约束**：一次请求完整提交通道的所有算法配置，数据库事务内原子校验总算力池（1000 点），失败整体回滚，彻底消除客户端 diff 的并发不一致与算力假阳性超限。
3. **分层解耦与平滑联动**：
   - 数据层：SQLite 迁移脚本双向平滑升级；
   - 宿主与 Native 层：`appBrokerPathResolver` 基于通道及算法活跃集合解析 FrameBroker socket 路径，取通道内活跃算法的 `max(sample_fps)` 作为物理发布帧率；
   - 跨域通知：变更后向所有增删改的算法 ID 广播 `OnTaskChanged`，触发热重启与订阅重挂载。
4. **前端极简契约**：前端配置弹窗单次 API 提交，废弃复杂的双阶段 Diff 逻辑。

---

## 2. 数据库设计与平滑迁移 (Database & Migration `000013`)

### 2.1 表结构定义

#### 主表：`analysis_tasks` (通道分析任务聚合根)
```sql
CREATE TABLE IF NOT EXISTS analysis_tasks (
    id TEXT PRIMARY KEY NOT NULL,
    camera_id TEXT NOT NULL UNIQUE,
    enabled BOOLEAN NOT NULL DEFAULT 1,
    name TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_analysis_tasks_camera_id ON analysis_tasks(camera_id);
```

#### 子表：`task_algorithms` (算法配置明细)
```sql
CREATE TABLE IF NOT EXISTS task_algorithms (
    id TEXT PRIMARY KEY NOT NULL,
    task_id TEXT NOT NULL REFERENCES analysis_tasks(id) ON DELETE CASCADE,
    algorithm_id TEXT NOT NULL REFERENCES algorithms(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT 1,
    sample_fps INTEGER NOT NULL DEFAULT 5,
    config_json TEXT NOT NULL DEFAULT '{}',
    region_config_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(task_id, algorithm_id)
);

CREATE INDEX IF NOT EXISTS idx_task_algorithms_task_id ON task_algorithms(task_id);
CREATE INDEX IF NOT EXISTS idx_task_algorithms_algorithm_id ON task_algorithms(algorithm_id);
```

**现状偏差（待本任务修正）**：`region_config_json` 目前由迁移 `000012_add_region_config_to_analysis_tasks` 落在**主表 `analysis_tasks`** 上（见 `internal/task/model.go`），与本节子表 `task_algorithms` 的定义不一致；本任务实现时需将该列**重新归位（re-home）到子表 `task_algorithms`**，与上表结构对齐。

### 2.2 迁移与回滚策略 (`000013_rebuild_channel_tasks.up.sql` / `.down.sql`)
1. **Up 迁移流程**：
   - 创建 `task_algorithms` 表；
   - 创建临时主表 `analysis_tasks_new`；
   - 从旧 `analysis_tasks` 按 `camera_id` 分组聚合插入 `analysis_tasks_new`（`id` 生成为 `task_` || `camera_id`，`enabled` 取 `MAX(enabled)`）；
   - 从旧 `analysis_tasks` 插入所有记录至 `task_algorithms`（`task_id` 指向对应的 `task_` || `camera_id`）；
   - `DROP TABLE analysis_tasks`，重命名 `analysis_tasks_new` 为 `analysis_tasks`；
   - 建立索引。
2. **Down 回滚流程**：
   - 建立旧版 `analysis_tasks_old` 结构；
   - 通过 `JOIN task_algorithms ta ON ta.task_id = t.id` 平铺还原 `(camera_id, algorithm_id)` 扁平记录（`enabled` 计算为 `t.enabled AND ta.enabled`）；
   - 删除新主子表，恢复旧表与旧索引。

---

## 3. Go 后端领域模型与业务逻辑 (`internal/task`)

### 3.1 实体模型与 DTO

```go
type Task struct {
    ID          string          `gorm:"primaryKey;column:id" json:"id"`
    CameraID    string          `gorm:"column:camera_id;not null;uniqueIndex" json:"cameraId"`
    Enabled     bool            `gorm:"column:enabled;not null" json:"enabled"`
    Name        string          `gorm:"column:name;not null;default:''" json:"name"`
    CreatedAt   time.Time       `gorm:"column:created_at;not null" json:"createdAt"`
    UpdatedAt   time.Time       `gorm:"column:updated_at;not null" json:"updatedAt"`
    Algorithms  []TaskAlgorithm `gorm:"foreignKey:TaskID;constraint:OnDelete:CASCADE" json:"algorithms"`
}

type TaskAlgorithm struct {
    ID               string    `gorm:"primaryKey;column:id" json:"id"`
    TaskID           string    `gorm:"column:task_id;not null;index" json:"taskId"`
    AlgorithmID      string    `gorm:"column:algorithm_id;not null;index" json:"algorithmId"`
    Enabled          bool      `gorm:"column:enabled;not null" json:"enabled"`
    SampleFPS        int       `gorm:"column:sample_fps;not null;default:5" json:"sampleFps"`
    ConfigJSON       string    `gorm:"column:config_json;not null;default:'{}'" json:"configJson"`
    RegionConfigJSON string    `gorm:"column:region_config_json;not null;default:'{}'" json:"regionConfigJson"`
    CreatedAt        time.Time `gorm:"column:created_at;not null" json:"createdAt"`
    UpdatedAt        time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
}
```

### 3.2 算力预算原子核算算法

总算力上限为 1000 点。单路任务算力公式：
$$\text{TaskPoints} = \begin{cases} 
0, & \text{if } \text{Task.Enabled} = \text{false} \\ 
\sum_{ta \in \text{Algorithms}, ta.\text{Enabled}} (ta.\text{SampleFPS} \times \text{algo.ComputeCost}), & \text{if } \text{Task.Enabled} = \text{true}
\end{cases}$$

**保存校验流程（在同一个 DB 事务中）**：
1. 计算全库其他 enabled 通道任务的算力消耗总和 $P_{other}$；
2. 计算当前待保存任务的算力消耗 $P_{current}$；
3. 若 $P_{other} + P_{current} > 1000$，事务立即回滚，返回 409 `COMPUTE_BUDGET_EXCEEDED`；
4. 校验通过后，Upsert 主表 `analysis_tasks`，并在子表中全量差量同步（删除本次被剔除的算法、更新保留的算法、插入新增的算法），整体 Commit。

### 3.3 算法包卸载级联适配 (`internal/algorithm/store.go`)
- 算法包卸载时，执行：
  ```sql
  DELETE FROM task_algorithms WHERE algorithm_id = ?
  ```
  清空关联子项，若通道任务下无任何算法，保留通道主任务（空算法列表）或按需处理。

### 3.4 宿主流调度联动 (`internal/app/app.go`)
- `appBrokerPathResolver` 查询某个 `algorithmID` 当前需要的物理相机列表：
  ```sql
  SELECT DISTINCT t.camera_id, ta.sample_fps 
  FROM task_algorithms ta
  JOIN analysis_tasks t ON t.id = ta.task_id
  WHERE ta.algorithm_id = ? AND ta.enabled = 1 AND t.enabled = 1
  ```
- 计算每路相机的最大发布节流率：
  取该相机上所有已启用通道任务中已启用算法的 $\max(sample\_fps)$。

---

## 4. HTTP API 契约升级

### 4.1 核心端点定义

| Method | Path | 说明 |
| :--- | :--- | :--- |
| `GET` | `/api/v1/tasks` | 获取所有通道任务列表（嵌套 `algorithms: [...]`）。支持 `?camera_id=xxx` 和 `?algorithm_id=xxx` 过滤 |
| `GET` | `/api/v1/tasks/:id` | 获取指定通道任务详情 |
| `POST` | `/api/v1/tasks` | 创建通道分析任务（含算法子列表） |
| `PUT` | `/api/v1/tasks/:id` | 全量更新通道分析任务（原子更新主开关、名称及算法列表） |
| `DELETE` | `/api/v1/tasks/:id` | 删除通道分析任务（级联删除所有算法绑定并释放算力） |

### 4.2 请求/响应载荷示例 (PUT /api/v1/tasks/:id)

```json
{
  "cameraId": "cam_01",
  "name": "西大门主路监控",
  "enabled": true,
  "algorithms": [
    {
      "algorithmId": "algo_fire_01",
      "enabled": true,
      "sampleFps": 5,
      "configJson": "{\"confidence\": 0.8}"
    },
    {
      "algorithmId": "algo_person_01",
      "enabled": true,
      "sampleFps": 10,
      "configJson": "{}"
    }
  ]
}
```

---

## 5. 前端交互重构 (`web/src/features/tasks/`)

1. **统一数据状态**：
   - `tasksApi.getTasks()` 返回 `Task[]`（包含 `algorithms: TaskAlgorithm[]`）；
   - 卡片视图直接从单个 Task 读取当前通道启用的所有算法。
2. **重构保存逻辑**：
   - 彻底废弃 `useSyncChannelAlgorithms` 中的多轮 Promise.all 脆弱 diff 逻辑；
   - 替换为单一原子保存 Hook `useSaveChannelTask`，用户在 `ChannelTaskDialog` 点击保存时，直接将当前通道的最新草稿组装为一次 API 调用提交；
   - 前端保留 1000 点算力超限即时预校验与提示。
3. **视图升级**：
   - `ChannelTasksView` 呈现通道主开关与已启用的算法标签集合；
   - `TasksTable` 支持平铺或主子行展示各算法子项。

---

## 6. 测试与验证计划

1. **迁移测试**：
   - 验证 `000013` 的 up 和 down 脚本；
   - 构造存量单算法任务数据，执行迁移后验证转换正确性与完整性。
2. **Go 单元与集成测试**：
   - `internal/task/store_test.go`：测试原子事务保存、算力超限阻断（409）、级联删除与查询过滤；
   - `internal/task/service_test.go`：测试参数与 Schema 校验、重复算法项拦截、变更事件广播；
   - `internal/task/handler_test.go`：测试各 REST 接口的状态码与响应格式；
   - `internal/app/broker_resolver_test.go`：测试多算法下 FrameBroker 路径解析与节流率聚合计算。
3. **前端自动化测试**：
   - 验证 `ChannelTaskDialog` 组装 payload 并单次提交；
   - 验证 `ChannelTasksView` 和 `TasksTable` 正常渲染新结构数据；
   - 运行真实无头浏览器 Vitest 测试通过。
4. **全量收工检验**：
   - 运行 `make check`（前端 lint/type-check/test + native-test + api-docs + go-check）全绿。
