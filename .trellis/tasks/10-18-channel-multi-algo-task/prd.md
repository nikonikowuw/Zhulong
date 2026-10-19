# PRD: 通道分析任务多算法模型重构 (Channel Multi-Algorithm Task Model)

## Goal

将现存的“单任务单算法”（`UNIQUE(camera_id, algorithm_id)`）模型重构为业内成熟的**通道多算法分析任务模型**：
以视频通道（Camera）为主体建立任务聚合根，一个任务内可配置并启用多个算法项（包含各自的 `sample_fps`、`config_json` 等）。
通过原子事务完成任务与多算法的持久化与算力预算校验，彻底废除前端脆弱的多请求 diff 同步，并平滑承接 Native FrameBroker 的单流多算法订阅与后续 ROI/MASK 区域策略。

---

## Background：已确认的仓库事实

| 编号 | 事实 | 证据 |
| :--- | :--- | :--- |
| F1 | `analysis_tasks` 表强约束 `UNIQUE(camera_id, algorithm_id)`，每个记录仅代表 1 个相机与 1 个算法的绑定 | `internal/database/migrations/000007_create_algorithm_tables.up.sql:23-33` |
| F2 | 前端已在 UI 层面做了通道卡片聚合呈现（`ChannelTasksView`、`ChannelTaskDialog`），心智已完全是“按通道配置多个算法” | `web/src/features/tasks/components/channel-tasks-view.tsx`、`channel-task-dialog.tsx` |
| F3 | 前端保存时必须通过 `useSyncChannelAlgorithms` 进行客户端差量计算，逐个调用 Delete / Create / Update，缺少原子事务保证，极易产生部分失败与算力假阳性超限 | `web/src/features/tasks/hooks/use-sync-channel-algorithms.ts` |
| F4 | Native 引擎与 Go 宿主（`FrameBroker`）已天然支持单流多算法并发订阅，且由 `appBrokerPathResolver` 统一计算 `max(sample_fps)` 下发节流率 | `internal/app/app.go:334-430`、`native/include/Zhulong/engine.h` |
| F5 | 算法推理结果与审计日志均不外键依赖 `analysis_tasks.id`，迁移风险低。`recognitions` / `alarms` 关联 `camera_id` + `algorithm_id`；`captures` **只有 `camera_id`（无 `algorithm_id`）** | `internal/database/migrations/000011_create_perception_and_alarm_tables.up.sql` |
| F6 | 算力预算池以 1000 点为上限，公式为 $\sum (\text{compute\_cost} \times \text{sample\_fps})$，目前在单任务变更事务中按全表累加校验 | `internal/task/store.go:110-180` |
| F7 | 算法包卸载时需清理关联任务：`DELETE FROM analysis_tasks WHERE algorithm_id = ?`，需适配子项外键或级联删除 | `internal/algorithm/store.go:188` |
| F8 | 当前系统未引入任务时间段/排期（Schedule）概念，任务启停状态完全由 `enabled` 布尔值控制 | `internal/task/model.go` |

---

## Requirements

### R1. 数据库模型重构与平滑数据迁移 (Migration)
- 新增数据库迁移（`000013_rebuild_channel_tasks.up.sql` / `.down.sql`）。
  - ⚠️ **编号约束**：`000012` 已被 `000012_add_region_config_to_analysis_tasks`（ROI/MASK 区域策略任务）占用。`golang-migrate` 的 `iofs` 源对同版本号返回 `source.ErrDuplicateMigration`，撞号会直接导致启动失败。必须使用 `000013`。
- 确立 **单通道单任务（1 Camera : 1 Task）** 模型：
  - 主表 `analysis_tasks`：聚合根，拥有 `id`, `camera_id` (强约束 `UNIQUE(camera_id)`), `enabled`, `name`, `created_at`, `updated_at`。
  - 子表 `task_algorithms`：配置明细，拥有 `id`, `task_id` (外键级联 `REFERENCES analysis_tasks(id) ON DELETE CASCADE`), `algorithm_id` (外键级联 `REFERENCES algorithms(id) ON DELETE CASCADE`), `enabled`, `sample_fps`, `config_json`, `region_config_json`, `created_at`, `updated_at`，约束 `UNIQUE(task_id, algorithm_id)`。
  - ⚠️ **`region_config_json` 不是预留字段**：T1（`10-18-roi-mask-region-policy`）已交付该列的存储与消费链路，用户可能已配置 ROI/MASK。迁移**必须搬运该列历史数据**，否则升级会静默清空用户配置。
  - ⚠️ **聚合根 `id` 取 `MIN(id)` 复用旧 ULID 有前提**：客户端可自带 `id`（`service.go:85` 仅在空时生成），若历史行存在非 ULID 的 id，`MIN(id)` 可能取错。实现前需校验或改用 `ORDER BY created_at, id LIMIT 1`（见 `design.md §2.2`）。
- 平滑迁移逻辑：迁移脚本将历史 `analysis_tasks` 中的行自动聚合转换为主表（按 `camera_id` 聚合成一个任务，`enabled` 取 `MAX(enabled)`，聚合根 `id` 取该相机最早那条旧任务的既有 ULID）及子表明细行，确保升级不丢历史配置（含 `region_config_json`）。具体步骤与实测约束见 `design.md §2.2`。

### R2. 领域模型与原子算力预算控制 (Domain & Budget Transaction)
- `internal/task` 领域模型升级为聚合根 `Task` 与实体 `TaskAlgorithm`。
- 算力预算在保存任务时**一次性原子校验**：
  - 汇总该任务所有 `enabled=true` 且主任务 `enabled=true` 的算法消耗：$\sum (\text{algo.compute\_cost} \times \text{item.sample\_fps})$；
  - 结合系统中其他 enabled 通道任务的算力消耗，若总和 $> 1000$ 点，整单事务回滚并返回 409 `COMPUTE_BUDGET_EXCEEDED`。
- 避免 GORM 零值更新缺陷（严禁 default:true 标签）。

### R3. 领域事件与跨域联动 (Cross-Domain Notification)
- `TaskChangeListener` 升级：在任务创建、更新、删除后，通知所有受影响的 `algorithmID` 列表，使各算法 runner 能够防抖重载其订阅关系。
- `appBrokerPathResolver` 适配新存储结构，按通道与启用的算法子项精准构建 FrameBroker 订阅集与 `max(sample_fps)`。注意 `publishRateByCamera`（`internal/app/app.go:408-427`）也消费 `ListTasks` 的扁平字段，需一并适配。
- ⚠️ **同步 T1 区域策略产物的消费者**：`RegionConfigCache.Reload` 依赖 `ListTasks` 返回的 `CameraID` / `AlgorithmID` / `RegionConfigJSON` 三字段。存储拆分后若不同步适配，**区域过滤会静默失效**（不报错、无日志）。完整的交接点清单见 `design.md §3.4`。

### R4. REST API 契约升级与向后兼容 (API Contract)
- `POST /api/v1/tasks`：支持提交包含完整算法列表的通道任务 payload。
- `PUT /api/v1/tasks/:id`：支持全量原子替换/更新通道任务及其下属算法配置（无需客户端逐个 diff）。
- `GET /api/v1/tasks` & `GET /api/v1/tasks/:id`：返回包含 `algorithms: [...]` 的完整聚合结构。
- 保留 `/api/v1/tasks?camera_id=xxx` 等过滤参数。
- 对旧 `/api/v1/algorithms/tasks` 提供兼容处理或明确的废弃演进。

### R5. 前端交互升级与脆弱 Diff 清除 (Frontend Refactoring)
- 彻底移除 `useSyncChannelAlgorithms` 中的多轮 Promise.all 脆弱 diff 逻辑。
- 「配置通道任务」弹窗（`ChannelTaskDialog`）直接将勾选状态、FPS 和配置 JSON 组装为一个原子对象调用单次 API 保存。
- `ChannelTasksView` 和 `TasksTable` 适配新的聚合数据结构，展示通道级启用状态及挂载的算法子项。
- 保留前端前置 1000 点算力校验与超限友好提示。

---

## Acceptance Criteria

- [x] **AC1**：迁移 `000013` 可顺畅执行 `up` 与 `down`；历史存量任务数据平滑升级为“主表+子表”结构且零数据丢失（**含 `region_config_json` 的历史区域配置**）。
- [x] **AC2**：创建/更新通道任务为单次原子请求，在数据库事务内完成主表及多算法明细写入，任一算法校验失败或总算力超限时整体回滚。
- [x] **AC3**：算力超限（> 1000 点）时精准拦截，返回稳定机器码 `COMPUTE_BUDGET_EXCEEDED`；正常增删改查算力核算精准无偏差。
- [x] **AC4**：修改通道任务中勾选的算法或 sample_fps 后，受影响的算法 Runner 能准确实时建立或注销 FrameBroker 订阅，节流率自动提升为通道内最大采样率。
- [x] **AC5**：前端「配置通道任务」弹窗保存仅触发一次 API 提交，无任何客户端 diff 并发请求；保存成功后卡片与表格即时刷新。
- [x] **AC6**：算法包被卸载时，正确清理 `task_algorithms` 对应关联项，不留孤儿数据（注意 `internal/algorithm/store.go:188` 与 `internal/task/store.go:282` 是**两个不同包**的同类写死，不能只改一个）。
- [x] **AC7**：⚠️ **区域过滤在重构后仍然生效**：多算法通道任务下，每个算法子项能解析出各自的 `region_config_json`，`RegionConfigCache` 的快照构建正确。这是跨任务交接的关键回归项（见 `design.md §3.4.1`）。
- [x] **AC8**：全部单元测试与集成测试通过，执行 `make check` 全绿（前端 lint/type-check/test + native-test + api-docs + go-check）。

---

## Out of Scope
- **多任务时间调度/定时排期（Schedule / Time Period）**：不在本次重构范围内，任务由 enabled 手动控制。
- **算法流水线/DAG 级联拓扑（如检测接识别）**：各算法保持独立的并发流订阅模型，不引入节点依赖 DAG。
- **ROI/MASK 区域具体编辑交互**：区域数据模型已在 T1（`10-18-roi-mask-region-policy`）交付（含编辑交互与流底图），本任务只负责将 `region_config_json` 从主表搬到子表并保持其消费链路（`RegionConfigCache`）可用。
