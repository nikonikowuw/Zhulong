# Implementation Plan: 通道多算法分析任务模型重构

## 1. 实施阶段总览与检查清单

### Phase 1: 数据库迁移与数据兼容性 (Database Migration)
- [ ] **Step 1.1**: 编写迁移脚本 `000012_rebuild_channel_tasks.up.sql`
  - 创建 `task_algorithms` 子表与外键级联
  - 创建临时主表 `analysis_tasks_new`（含 `camera_id UNIQUE`）
  - 从历史 `analysis_tasks` 分组聚合数据插入新主表与子表
  - 替换表名并建立索引
- [ ] **Step 1.2**: 编写回滚脚本 `000012_rebuild_channel_tasks.down.sql`
  - 恢复旧结构并将主子表平铺还原为 `(camera_id, algorithm_id)` 扁平数据
- [ ] **Step 1.3**: 验证双向迁移
  - 验证命令：`go test -race -v ./internal/database/...`

### Phase 2: 后端实体模型与持久化层 (Model & Store)
- [ ] **Step 2.1**: 更新 `internal/task/model.go`
  - 定义 `Task`（主表聚合根）与 `TaskAlgorithm`（子表实体）
  - 定义请求载荷 `SaveChannelTaskRequest` 与 `TaskAlgorithmItem`
- [ ] **Step 2.2**: 重构 `internal/task/store.go`
  - 编写事务级原子保存方法 `SaveTaskWithinBudget`
  - 实现基于 1000 点总算力池的原子计算与拦截
  - 重写 `GetTask`、`GetTaskByCamera`、`ListTasks` 及级联删除
- [ ] **Step 2.3**: 完善 `internal/task/store_test.go`
  - 覆盖算力超限阻断、多算法保存、单算法更新、级联删除单测
  - 验证命令：`go test -race -v ./internal/task -run TestStore`

### Phase 3: 领域服务、跨域联动与宿主调度 (Service, Events & App Wiring)
- [ ] **Step 3.1**: 重构 `internal/task/service.go`
  - 校验各算法合法性、配置 Schema 及 FPS 范围
  - 聚合受影响的算法 ID 集合，广播 `TaskChangeListener.OnTaskChanged`
- [ ] **Step 3.2**: 适配算法包卸载逻辑 `internal/algorithm/store.go`
  - 卸载算法时清理 `task_algorithms` 关联行
- [ ] **Step 3.3**: 适配宿主流订阅解析 `internal/app/app.go` (`appBrokerPathResolver`)
  - 基于新模型查询指定算法关联的相机列表与 `max(sample_fps)`
- [ ] **Step 3.4**: 补齐与修复关联测试
  - 修复 `internal/task/service_test.go`、`internal/app/broker_resolver_test.go`、`internal/algorithm/service_task_test.go`
  - 验证命令：`go test -race -v ./internal/task/... ./internal/algorithm/... ./internal/app/...`

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

- **数据库迁移失败回滚**：`000012` 包含全量平铺还原逻辑，若升级遇错执行 `make migrate-down` 回滚至 `000011`。
- **Git 隔离**：所有改动严格在当前分支进行，每个 Phase 均有独立可运行的测试守护，保持可随时提交与中断。
