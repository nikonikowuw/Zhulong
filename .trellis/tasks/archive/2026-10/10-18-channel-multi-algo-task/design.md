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

> ⚠️ **编号约束**：`000012` 已被 `000012_add_region_config_to_analysis_tasks`（ROI/MASK 区域策略任务）占用。
> `golang-migrate` 的 `iofs` 源对同版本号返回 `source.ErrDuplicateMigration`（`migrate/v4@v4.20.1/source/iofs/iofs.go:69`），
> 撞号会直接导致**启动失败**。本任务必须使用 `000013`，不得回退到 `000012`。

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

⚠️ **该列不是「预留」字段，而是已上线且有真实数据**：T1（`10-18-roi-mask-region-policy`）已交付该列的存储、校验、快照缓存与过滤链路，用户可能已经配置了 ROI/MASK。因此迁移**必须搬运该列的历史数据**，否则升级会静默清空用户配置（详见 §2.2）。

### 2.2 迁移与回滚策略 (`000013_rebuild_channel_tasks.up.sql` / `.down.sql`)

#### ⚠️ 三条必须遵守的实测约束

原方案（建子表 → 插新主表 → 插子表 → DROP → RENAME）**跑不通**，以下均为实测复现结论：

1. **子表的 `INSERT` 必须在 `DROP` + `RENAME` 之后**。否则 `task_id` 指向的是 `analysis_tasks_new`，而 FK 声明指向 `analysis_tasks`（此时仍是旧表），直接报 `FOREIGN KEY constraint failed`。
2. **不能先 `DROP` 再插子表** —— `task_algorithms.task_id` 带 `ON DELETE CASCADE`，`DROP TABLE analysis_tasks` 会把刚插入的子表行**全部清空**（实测：DROP 后子表行数 0）。
3. **必须先备份（或重命名）旧表**。由 1 与 2 夹击，正确顺序是「备份 → 迁移 → DROP → RENAME → 再插子表」，而插入子表时需要读旧数据，那时旧表已被 DROP，因此**本方案下必须提前保留一份旧数据**（`CREATE TABLE temp_old_tasks AS SELECT * FROM analysis_tasks`；或改用 `ALTER TABLE analysis_tasks RENAME TO temp_old_tasks` 重命名，效果等价且省一次拷贝）。

⚠️ **`PRAGMA foreign_keys = OFF` 不可用**：golang-migrate 将每条迁移包在事务内执行，SQLite 在事务内忽略该 pragma。该结论已在 `000009_decouple_person_id.up.sql` 明文记录，本次实测二次确认。

#### Up 迁移流程（已验证顺序）

1. `CREATE TABLE temp_old_tasks AS SELECT * FROM analysis_tasks`（备份，含 `region_config_json`）；
2. 创建 `task_algorithms` 表（含 `region_config_json`）；
3. 创建新主表 `analysis_tasks_new`；
4. 从 `temp_old_tasks` 按 `camera_id` 分组插入新主表 —— **聚合根 `id` 取 `MIN(id)`**，即复用该相机最早那条旧任务的既有 ULID（理由见下）；`enabled` 取 `MAX(enabled)`；
5. `DROP TABLE analysis_tasks`（此时子表仍为空，无级联损失）；
6. `ALTER TABLE analysis_tasks_new RENAME TO analysis_tasks`（FK 目标就位）；
7. **从 `temp_old_tasks` JOIN 新主表插入 `task_algorithms`**，`region_config_json` 一并搬运：

   ```sql
   INSERT INTO task_algorithms (id, task_id, algorithm_id, enabled, sample_fps, config_json, region_config_json)
   SELECT o.id, n.id, o.algorithm_id, o.enabled, o.sample_fps, o.config_json, o.region_config_json
   FROM temp_old_tasks o
   JOIN analysis_tasks n ON n.camera_id = o.camera_id;
   ```

8. `DROP TABLE temp_old_tasks`；
9. 建立索引。

#### 聚合根主键：复用既有 ULID，不拼接伪主键

❌ 原方案 `id = 'task_' || camera_id` **违反仓库明文规范**：`database-guidelines.md §4.5` 要求「严禁伪语义拼接复合主键……严禁拼接形如 `task_{cam}_{algo}` 的超长混合串」。

✅ 改用 `MIN(id)`（该相机最早那条旧任务的 ULID）。理由：

- 迁移 SQL 内**无法生成 ULID** —— Crockford Base32 无对应 SQL 函数，迁移文件中也无任何 id 生成先例（`grep randomblob/hex/lower` 无结果）；
- 旧表 `analysis_tasks.id` 通常本身就是 `idutil.New()` 产出的 26 字符 ULID；
- ULID 前 10 字符是大端 base32 时间戳，因此**字典序等价于时间序**，`MIN(id)`（SQLite 的 `MIN` 用 BINARY collation）确实命中最早那条。

⚠️ **前提缺口（实现时必须处理）**：上述论断默认旧表所有 `id` 都是 `idutil.New()` 产物，但**客户端可自带 `id`** —— `internal/task/service.go:85-86` 仅在 `t.ID == ""` 时生成，`model.go:112` 会直接采纳 `req.ID`。若历史行存在非 ULID / 非 26 字符的 id，`MIN(id)` 可能不是最早那条，甚至产出非法 ULID。

应对（二选一）：

- 迁移前加 `id` 合法性前置校验（不合法则改用下方方案）；或
- 改用 `ORDER BY created_at ASC LIMIT 1` 取最早任务的 id（但 SQLite 的 `CURRENT_TIMESTAMP` 只有秒精度，同一秒内创建的多条任务排序不确定，需再以 `id` 做次级排序）。

⚠️ **取舍**：聚合根 id 与某条旧任务 id 语义重叠。实现时需确认无外部系统引用旧任务 id（当前 `recognitions` / `alarms` 只关联 `camera_id` + `algorithm_id`，`captures` 只有 `camera_id`，均不外键依赖 `analysis_tasks.id`，见本任务 PRD F5），否则应改为在 Go 侧生成新 ULID 的补数据迁移。

#### Down 回滚流程

1. 创建旧版 `analysis_tasks_old` 结构（含 `region_config_json`）；
2. 通过 `JOIN` 平铺还原 `(camera_id, algorithm_id)` 记录：

   ```sql
   INSERT INTO analysis_tasks_old (id, camera_id, algorithm_id, enabled, sample_fps, config_json, region_config_json)
   SELECT ta.id, t.camera_id, ta.algorithm_id, (t.enabled AND ta.enabled),
          ta.sample_fps, ta.config_json, ta.region_config_json
   FROM task_algorithms ta
   JOIN analysis_tasks t ON t.id = ta.task_id;
   ```

   `enabled` 计算为 `t.enabled AND ta.enabled`（任务级主开关与算法级子开关的合取），`region_config_json` 从子表搬回主表；
3. 删除新主子表，恢复旧表与旧索引。

#### 已验证的往返行为

以「cam_1 两条（一条 `enabled=0`）+ cam_2 一条」为样本实测：

- **up**：3 条旧记录 → 2 主 + 3 子，`region_config_json` 全部保留，`PRAGMA foreign_key_check` 无违规；
- **down**：还原 3 条扁平记录，任务级停用的子项 `enabled` 正确保持 0；
- **算法卸载**：删除 `algorithms` 行后子表 FK `CASCADE` 自动清理，空算法列表的通道任务保留。

#### 迁移验收

- 构造存量单算法/多算法任务数据，断言迁移后 `region_config_json` **零丢失**（这是原方案会静默违反的一条）。

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
- 算法包卸载时，当前逻辑写死主表列：
  ```go
  // 现状 internal/algorithm/store.go:188 —— 重构后该列不再存在于主表，此语句会失败
  db.Exec("DELETE FROM analysis_tasks WHERE algorithm_id = ?", algorithmID)
  ```
- 重构后应改为清理子表：
  ```sql
  DELETE FROM task_algorithms WHERE algorithm_id = ?
  ```
  子表 `algorithm_id` 带 `REFERENCES algorithms(id) ON DELETE CASCADE`，FK 级联亦会自动完成清理（实测确认）；显式删除用于兼容未开 FK 的场景。清空后若通道任务下无任何算法，**保留通道主任务**（空算法列表），不级联删除主表。

### 3.4 ⚠️ 必须同步的交接点清单

> 本节是交接清单。本任务把 `(camera_id, algorithm_id)` 扁平结构拆成主子表，
> 会同时破坏两类既有代码：① T1（`10-18-roi-mask-region-policy`）已上线的区域策略产物；
> ② 本任务自身重构直接波及的扁平结构依赖。
>
> ⚠️ **漏改的后果分两种，都必须逐项处理**：引用旧列的 SQL 会**静默失效或查询报错**（`RegionConfigCache` 静默失效最难发现）；而 `app.go` 直接读 `tsk.SampleFPS` 这类是**编译期**依赖，字段搬走会直接编译失败。

#### 3.4.1 T1 区域策略产物（跨任务交接）

| 交接点 | 位置 | 为什么必须改 | 本任务需做的改动 |
| :--- | :--- | :--- | :--- |
| `RegionConfigCache.Reload` | `internal/task/region_config.go:62-99` | 它遍历 `store.ListTasks()` 的返回值读取 `t.CameraID` / `t.AlgorithmID` / `t.RegionConfigJSON` 三字段来构建快照。重构后这三个字段分居主表与子表，`ListTasks` 若仍返回旧的扁平 `Task` 结构，快照将拿不到算法归属与区域配置 | 改为从主+子表 JOIN 取 `(cameraID, algorithmID, regionConfigJSON)` 三元组；缓存键 `RegionKey{CameraID, AlgorithmID}` 语义不变 |
| `ListTasks` 返回结构 | `internal/task/store.go:200-210` | 目前是单表 `db.Find(&list)`；重构后需带出 `Algorithms` 子项。⚠️ **有两个消费者**：`RegionConfigCache`（上一条）与 `appBrokerPathResolver.publishRateByCamera`（`internal/app/app.go:408-427`，读 `tsk.Enabled` / `tsk.SampleFPS` / `tsk.CameraID` 算每相机最大发布节流率） | 预加载子表（`Preload("Algorithms")` 或等价 JOIN），或另提供一个扁平查询专供缓存使用（后者可避免缓存为读快照而加载全部子项）。`publishRateByCamera` 需同步适配为「取该相机下所有 enabled 子项的 `max(sample_fps)`」 |
| 迁移测试断言 | `internal/database/store_test.go:846` | `TestRegionConfigMigrationUpAndDownSymmetry` 硬断言 `region_config_json` 在**主表**上，且 `migrator.Migrate(12)` | 断言改为子表 `task_algorithms`；迁移目标版本改为 `13` |

⚠️ **验收要求**：本任务落地后，`RegionConfigCache` 必须仍能正确解析出区域配置。建议在 `internal/task/region_config_test.go` 中补一条「多算法通道任务下，每个算法子项各自解析出自己区域配置」的用例 —— 现有用例只覆盖了单算法场景（`TestRegionConfigCache` / `TestRegionConfigCacheOnTaskChanged`）。

#### 3.4.2 本任务自身波及的扁平结构依赖

| 交接点 | 位置 | 为什么必须改 | 本任务需做的改动 |
| :--- | :--- | :--- | :--- |
| `validateComputeBudget` 算力核算 | `internal/task/store.go:105,117-124` | 直接 `Select("analysis_tasks.sample_fps")` + `JOIN algorithms ON algorithms.id = analysis_tasks.algorithm_id`。重构后这两列分居主子表，该查询会**直接报错**（由 `CreateTaskWithinBudget` / `UpdateTaskWithinBudget` 调用） | 改为 `task_algorithms JOIN algorithms`（子表带 `algorithm_id` 与 `sample_fps`），并同时满足主表 `enabled` 与子表 `enabled` |
| `GetTaskByCameraAndAlgorithm` | `internal/task/store.go:184,190` | `Where("camera_id = ? AND algorithm_id = ?")` 按「相机+算法」取单条；新模型下 `camera_id` 唯一，该查询语义已不成立 | 改为按 `camera_id` 取聚合根并预加载子项；调用方需同步适配（或新增「取聚合根下某个算法子项」的查询） |
| `DeleteTasksByAlgorithm` | `internal/task/store.go:277,282` | `db.Where("algorithm_id = ?").Delete(&Task{})` 按 `algorithm_id` 删主表 | 改为删 `task_algorithms` 子表 |
| 算法包卸载清理 | `internal/algorithm/store.go:188` | 写死 `DELETE FROM analysis_tasks WHERE algorithm_id = ?`，重构后主表已无 `algorithm_id` 列。与上一行是**两个不同包的同类写死**，极易只改一个 | 改删 `task_algorithms` 子表（见 §3.3）；或统一收敛到单一实现供两个调用方复用 |

⚠️ **本表可能仍不完整**：实现时请以 `grep -rn "analysis_tasks" internal/` 与 `grep -rn "algorithm_id = ?" internal/` 的实际结果为准，不要只依赖本表。

### 3.5 宿主流调度联动 (`internal/app/app.go`)
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
   - 构造存量单算法/多算法任务数据，执行迁移后验证转换正确性与完整性；
   - ⚠️ **断言 `region_config_json` 零丢失**（原方案会静默清空，见 §2.2）；
   - ⚠️ **断言 up/down 往返后 `enabled` 语义正确**（任务级停用的子项必须仍为 0）。
2. **Go 单元与集成测试**：
   - `internal/task/store_test.go`：测试原子事务保存、算力超限阻断（409）、级联删除与查询过滤；
   - `internal/task/service_test.go`：测试参数与 Schema 校验、重复算法项拦截、变更事件广播；
   - `internal/task/handler_test.go`：测试各 REST 接口的状态码与响应格式；
   - `internal/app/broker_resolver_test.go`：测试多算法下 FrameBroker 路径解析与节流率聚合计算（含 `publishRateByCamera` 对新结构的适配）；
   - `internal/algorithm/service_test.go`：`TestServiceDeleteAlgorithmCleansUpTasks`（:33）直接 `INSERT INTO analysis_tasks` 并断言 `WHERE algorithm_id = ?` 计数为 0，需改为主子表结构；
   - `internal/algorithm/service_task_test.go`：同步修复；
   - ⚠️ **`internal/task/region_config_test.go`**：补「多算法通道任务下各子项区域配置独立解析」用例，守护 §3.4 登记的交接点；
   - ⚠️ **`internal/database/store_test.go`**：`TestRegionConfigMigrationUpAndDownSymmetry` 的断言目标从主表改为子表。
3. **前端自动化测试**：
   - 验证 `ChannelTaskDialog` 组装 payload 并单次提交；
   - 验证 `ChannelTasksView` 和 `TasksTable` 正常渲染新结构数据；
   - ⚠️ 修复 `web/src/features/tasks/api/tasks-api.test.ts`（:44、:127 断言扁平 `algorithmId` / `sampleFps` 结构）；
   - 运行真实无头浏览器 Vitest 测试通过。
4. **全量收工检验**：
   - 运行 `make check`（前端 lint/type-check/test + native-test + api-docs + go-check）全绿。
