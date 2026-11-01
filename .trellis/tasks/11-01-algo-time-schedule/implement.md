# 实施计划与步骤: 算法分时调度与时间计划 (Implementation Plan)

## 阶段划分

### 阶段 1: 核心领域与计划解析包 (`internal/schedule`)
- [x] 创建 `internal/schedule` 包：定义 `TimeRange`, `WeekSchedule`；
- [x] 实现 JSON 序列化、反序列化、格式校验与相邻区间合并归一化；
- [x] 实现 `IsActiveAt(t time.Time)` 状态判断方法；
- [x] 实现基于扫描线（Sweep-line）的 `CalculatePeakPoints` 峰值算力计算；
- [x] 编写全面的单元测试（100% 覆盖率边界条件：跨周日周一、重叠时段、零长度区间等）。

### 阶段 2: 数据库迁移与任务领域集成 (`internal/task`)
- [x] 新建迁移 `000025_add_schedule_json_to_task_algorithms.up.sql` 与 `.down.sql`；
- [x] 在 `internal/task/model.go` 的 `TaskAlgorithm` 和 `TaskAlgorithmItem` 中添加 `ScheduleJSON`；
- [x] 改造 `internal/task/store.go` 中的 `validateComputeBudget`：利用 `internal/schedule` 算法替代原先的粗暴加和，支持分时峰值算力校验；
- [x] 在 `internal/task/service.go` 中实现 `ScheduleWorker`，在启动时运行并在时刻翻转时触发 `TaskChangeListener`；
- [x] 更新 `internal/app/adapters.go` 的 `SyncBrokerSubscriptions`：只为当前处于有效排程内的任务分配媒体流；
- [x] 编写与更新 Go 测试（`store_test.go`, `service_test.go`, `broker_resolver_test.go`）。

### 阶段 3: 前端 7×24h 网格与表单集成 (`web/src/features/tasks`)
- [x] 封装通用 `ScheduleGrid` 组件（7×48 单元格、鼠标拖拽刷取、快捷填充预设）；
- [x] 在 `ChannelTaskAlgoPanel` 的算法配置面板中嵌入运行计划抽屉/面板；
- [x] 更新 `use-channel-task-drafts.ts` 与 `task-algorithm-payload.ts`，支持 `scheduleJson` 的读取、序列化与提交；
- [x] 改造 `ChannelTaskBudgetMeter` 与算力计算 hook，支持展示峰值算力与分时复用提示；
- [x] 编写前端组件与状态测试（`schedule-grid.test.tsx`, `use-channel-tasks.test.ts`）。

### 阶段 4: 质量门禁与端到端验证
- [x] 运行 `make check`（包含 native-test、api-docs、前端 lint/test、Go `go test -race`）；
- [x] 验证分时排程在 RK3568/主机环境下的算力分配与热切换行为。
