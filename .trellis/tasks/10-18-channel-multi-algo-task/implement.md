# Implementation Plan: 通道多算法分析任务模型重构

## 1. 实施阶段总览与检查清单

### Phase 1: 数据库迁移与数据兼容性 (Database Migration)
> ⚠️ **编号约束**：`000012` 已被 ROI/MASK 区域策略任务占用（`000012_add_region_config_to_analysis_tasks`）。本任务必须使用 `000013`。
- [ ] **Step 1.1**: 编写迁移脚本 `000013_rebuild_channel_tasks.up.sql`
  - **备份旧表** `CREATE TABLE temp_old_tasks AS SELECT * FROM analysis_tasks`（必须在 DROP 之前，子表插入需要读旧数据）
  - 创建 `task_algorithms` 子表（双 FK 级联：`analysis_tasks` 与 `algorithms`，含 `region_config_json`）
  - 创建临时主表 `analysis_tasks_new`（含 `camera_id UNIQUE`）
  - 从 `temp_old_tasks` 分组聚合插入新主表：聚合根 `id` 取 `MIN(id)`（复用既有 ULID，**不得**用 `'task_' || camera_id` 拼接伪主键）
  - `DROP TABLE analysis_tasks` → `ALTER TABLE analysis_tasks_new RENAME TO analysis_tasks`（顺序不可颠倒，否则 FK 报错或 CASCADE 清空）
  - **之后再**从 `temp_old_tasks` JOIN 新主表插入子表，`region_config_json` 一并搬运
  - 删除 `temp_old_tasks` 并建立索引
  - ⚠️ 不得依赖 `PRAGMA foreign_keys = OFF`（golang-migrate 在事务内执行，pragma 无效）
  - 完整步骤与实测依据见 `design.md §2.2`
- [ ] **Step 1.2**: 编写回滚脚本 `000013_rebuild_channel_tasks.down.sql`
  - 恢复旧结构并将主子表平铺还原为 `(camera_id, algorithm_id)` 扁平数据
  - `enabled = t.enabled AND ta.enabled`；`region_config_json` 从子表搬回主表
- [ ] **Step 1.3**: 验证双向迁移
  - ⚠️ 断言 `region_config_json` 零丢失，且 up/down 往返后 `enabled` 语义正确
  - 验证命令：`python3 native/scripts/build.py go test -race ./internal/database/...`

### Phase 2: 后端实体模型与持久化层 (Model & Store)
- [ ] **Step 2.1**: 更新 `internal/task/model.go`
  - 定义 `Task`（主表聚合根）与 `TaskAlgorithm`（子表实体）
  - 定义请求载荷 `SaveChannelTaskRequest` 与 `TaskAlgorithmItem`
- [ ] **Step 2.2**: 重构 `internal/task/store.go`
  - 编写事务级原子保存方法（现有 `CreateTaskWithinBudget` / `UpdateTaskWithinBudget` 需扩展为支持多算法子项）
  - 实现基于 1000 点总算力池的原子计算与拦截
  - 重写 `GetTask`、`GetTaskByCameraAndAlgorithm`、`ListTasks` 及级联删除
- [ ] **Step 2.3**: 完善 `internal/task/store_test.go`
  - 覆盖算力超限阻断、多算法保存、单算法更新、级联删除单测
  - 验证命令：`go test -race -v ./internal/task -run TestStore`

### Phase 3: 领域服务、跨域联动与宿主调度 (Service, Events & App Wiring)
- [ ] **Step 3.1**: 重构 `internal/task/service.go`
  - 校验各算法合法性、配置 Schema 及 FPS 范围
  - 聚合受影响的算法 ID 集合，广播 `TaskChangeListener.OnTaskChanged`
- [ ] **Step 3.2**: 适配算法包卸载逻辑 `internal/algorithm/store.go`
  - 卸载算法时清理 `task_algorithms` 关联行（现写死 `DELETE FROM analysis_tasks WHERE algorithm_id = ?`，重构后主表已无该列）
- [ ] **Step 3.3**: ⚠️ **同步交接点清单**（详见 `design.md §3.4`）
  - **T1 区域策略产物**：`RegionConfigCache.Reload`（`internal/task/region_config.go`）改为从主+子表 JOIN 取三元组，否则**区域过滤静默失效**；`ListTasks`（`internal/task/store.go`）带出子项；`internal/database/store_test.go` 的 `TestRegionConfigMigrationUpAndDownSymmetry` 断言目标改子表且 `Migrate(12)`→`Migrate(13)`；补 `internal/task/region_config_test.go` 多算法子项用例
  - **本任务自身波及**：`validateComputeBudget` 算力核算（`store.go:105,117-124`）改为查 `task_algorithms JOIN algorithms`；`GetTaskByCameraAndAlgorithm`（`store.go:184,190`）改按 `camera_id` 取聚合根；`DeleteTasksByAlgorithm`（`store.go:277,282`）改删子表；`appBrokerPathResolver.publishRateByCamera`（`internal/app/app.go:408-427`）适配新结构算每相机最大节流率
- [ ] **Step 3.4**: 适配宿主流订阅解析 `internal/app/app.go` (`appBrokerPathResolver`)
  - 基于新模型查询指定算法关联的相机列表与 `max(sample_fps)`
- [ ] **Step 3.5**: 补齐与修复关联测试
  - 修复 `internal/task/service_test.go`、`internal/app/broker_resolver_test.go`、`internal/algorithm/service_test.go`（`TestServiceDeleteAlgorithmCleansUpTasks`）、`internal/algorithm/service_task_test.go`
  - 前端 `web/src/features/tasks/api/tasks-api.test.ts`（扁平结构断言）
  - 验证命令：`python3 native/scripts/build.py go test -race ./internal/task/... ./internal/algorithm/... ./internal/app/...`

### Phase 4: HTTP Handler 与 API 文档 (Handler & OpenAPI Docs)
- [ ] **Step 4.1**: 更新 `internal/task/handler.go`
  - 更新 `POST /api/v1/tasks`、`PUT /api/v1/tasks/:id`、`GET /api/v1/tasks` DTO
  - 保留并优雅适配兼容路由
- [ ] **Step 4.2**: 更新 `internal/task/handler_test.go`
- [ ] **Step 4.3**: 重新生成 OpenAPI 文档
  - 验证命令：`make api-docs`

### Phase 5: 前端模型、API 客户端与页面交互重构 (Frontend)
- [ ] **Step 5.1**: 更新 TypeScript 类型定义与 API 客户端
  - `web/src/features/tasks/data/schema.ts`：定义 `Task` 与 `TaskAlgorithm`
  - `web/src/features/tasks/api/tasks-api.ts`：单次原子请求接口
- [ ] **Step 5.2**: 废除脆弱 Diff 并重构保存 Hook
  - 替换 `useSyncChannelAlgorithms` 为单次提交的 `useSaveChannelTask`
- [ ] **Step 5.3**: 更新「配置通道任务」弹窗 `ChannelTaskDialog.tsx`
  - 保存时直接组装当前通道任务原子载荷提交
- [ ] **Step 5.4**: 更新卡片视图与表格
  - `ChannelTasksView.tsx` 适配新结构
  - `TasksTable.tsx` 支持多算法明细呈现
- [ ] **Step 5.5**: 修复前端测试
  - 验证命令：`cd web && pnpm type-check && pnpm test`

### Phase 6: 全局质量验收 (Final Quality Gate)
- [ ] **Step 6.1**: 全量自动化检查全绿
  - 验证命令：`make check`
  - 包括：Go 单测、数据竞争检查（-race）、前端 lint/type-check/test、API 文档同步等。

---

## 2. 关键回滚与断点机制 (Rollback Plan)

- **数据库迁移失败回滚**：`000013` 包含全量平铺还原逻辑，若升级遇错执行 `make migrate-down` 回滚至 `000012`。
- **Git 隔离**：所有改动严格在当前分支进行，每个 Phase 均有独立可运行的测试守护，保持可随时提交与中断。

## 3. 验证命令速查

```bash
# Go（凡(间接)依赖 internal/engine 的包一律走 wrapper，不要裸跑 go test）
python3 native/scripts/build.py go test -race ./internal/database/...
python3 native/scripts/build.py go test -race ./internal/task/... ./internal/algorithm/... ./internal/app/...

# 收工门禁
make check
```

## 4. 开工前必须复核的既有事实

- [ ] `000013` 未被占用（`ls internal/database/migrations/ | grep 000013`）
- [ ] `region_config_json` 在主表上的现存数据量（若为 0，搬运逻辑仍需保留，但风险等级下降）
- [ ] 交接点清单（`design.md §3.4`）已全部纳入实现计划，并以 `grep -rn "analysis_tasks" internal/` 复核无遗漏
- [ ] `MIN(id)` 复用既有 ULID 的前提成立：确认无外部系统引用旧任务 id（当前感知事件只关联 `camera_id` + `algorithm_id`）
