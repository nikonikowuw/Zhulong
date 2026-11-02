# PRD: 算法分时调度与时间计划 (Algorithm Time Scheduling & Peak Compute Budget)

## 1. 背景与目标 (Goal)

Zhulong 作为边缘 AI 运维宿主（如 RK3568 SoC 平台），NPU 算力上限（1,000 点）与发热功耗受到严格物理约束。目前系统中的分析任务默认是 24×7 全天候运行。
引入**算法子项（TaskAlgorithm）分时计划调度**与**分时动态算力预算控制**：
1. **分时复用**：支持为摄像头的各个算法独立配置 7×24h 运行时间表（如白天跑客流/人脸，夜间跑周界入侵）；
2. **动态预算池**：放开静态全时段累加限制，改为校验**任意时间切片内的瞬时最大算力峰值不超过 1,000 点**，真正释放边缘多算法复用算力能力；
3. **自动化生命周期**：到达设定的启用/休眠时段时，调度器自动联动现有 `TaskChangeListener` 与 `Supervisor`，动态停止或拉起算法 Runner 进程并挂载/卸载媒体流。

---

## 2. 需求清单 (Requirements)

### 2.1 数据与存储 (Schema & Persistence)
- **R1.1 数据库迁移**：新增迁移 `000025_add_schedule_json_to_task_algorithms`，在 `task_algorithms` 表中追加字段 `schedule_json TEXT NOT NULL DEFAULT '{}'`。
- **R1.2 计划数据结构**：
  - 格式采用规范化时间区间 JSON，按星期（0=周日，1..6=周一至周六，或语义化键名 `monday..sunday`）定义时间区间 `[{"start": "HH:mm", "end": "HH:mm"}]`。
  - 空 JSON `{}` 或未启用表示 24×7 全天候运行（向后兼容）。
- **R1.3 数据校验**：区间格式校验（`start < end`，无重叠或自动归一化合并），时间范围在 00:00 至 24:00 之间。

### 2.2 瞬时动态算力预算 (Dynamic Peak Compute Budget)
- **R2.1 峰值计算算法**：在 `internal/task`（或抽出的 `internal/schedule` 工具包）中实现基于扫描线（Sweep-line）的时间区间峰值算力统计。
- **R2.2 事务内防超限拦截**：在 `SaveTaskWithinBudget` 与 `CreateTaskWithinBudget` 中，将原先简单的全量静态相加改为：
  $$\max_{t \in [0, 7 \times 24\text{h})} \sum_{i \in \text{Active}(t)} (\text{SampleFPS}_i \times \text{ComputeCost}_i) \le 1000$$
  超限时依然返回 409 `COMPUTE_BUDGET_EXCEEDED`。

### 2.3 调度引擎与状态联动 (Scheduler & Runner Lifecycle)
- **R3.1 调度 Worker**：在 `internal/task` 中新增后台周期性检查 Worker（`ScheduleWorker`），每分钟或按下次事件点触发。
- **R3.2 时区对齐**：使用平台统一配置的系统本地时间（读取 `systemtime` 或 `time.Now()`，遵循系统 IANA 时区设置）。
- **R3.3 状态驱动契约**：
  - 当某算法的有效状态（`task.Enabled && taskAlgorithm.Enabled && inSchedule(now)`）在时间点发生翻转时，触发 `TaskChangeListener.OnTaskChanged(ctx, algorithmID)`。
  - `SyncBrokerSubscriptions` 根据此时刻算法的有效性，动态 attach/release AI 媒体流；
  - 当算法在所有通道均处于非排程休眠时段时，`Supervisor` 自动将进程置为待命（Standby）状态，释放 NPU 资源。

### 2.4 前端交互 (UI & UX)
- **R4.1 7×24 小时网格编辑器**：在算法配置面板中提供安防标准的 7×24h 网格（支持 30 分钟或 1 小时为 1 个单元格），支持鼠标拖拽刷取、框选、反选、快捷填充（全选、工作日白天、夜间布防等）。
- **R4.2 算力条与状态展示**：
  - 算力占用仪表盘（Budget Meter）显示当前通道及全平台「峰值算力（Peak）」与「当前瞬时算力（Current）」；
  - 任务列表/算法卡片直观标示「运行中（计划内）」或「休眠中（非计划时段）」。

---

## 3. 验收标准 (Acceptance Criteria)

- [x] **AC1 存储与向后兼容**：执行迁移后，既有任务算法默认为全天候 24×7 运行，已有测试全部通过；
- [x] **AC2 峰值算力校验准确性**：
  - 两个各占 600 点的算法，若排程完全不重叠（如 A 为 08:00-18:00，B 为 18:00-08:00），峰值为 600 点，允许保存成功；
  - 若 A 与 B 存在哪怕 1 分钟的重叠时段（重叠期点数达到 1200），拒绝保存并返回 409；
- [x] **AC3 运行时自动切换**：跨过计划启停边界时，后端日志输出流挂载/卸载事件，`Supervisor` 相应起停算法 Runner；
- [x] **AC4 前端交互流畅**：在 7×24 网格上拖拽无卡顿，保存后刷新页面能完整还原选择的高亮色块；
- [x] **AC5 质量门禁**：通过 `make check`（Go 测试带 `-race`、前端 lint/test、native 编译）。
