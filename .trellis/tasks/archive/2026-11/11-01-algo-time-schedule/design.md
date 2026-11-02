# 技术架构设计: 算法分时调度与时间计划 (Design Specification)

## 1. 架构拓扑与交互链路

```
 ┌─────────────────────────────────────────────────────────────┐
 │                         Web Frontend                        │
 │  7×24h 网格刷子 (48格/天) ──> 转换为时间段 JSON ──> REST API │
 └──────────────────────────────┬──────────────────────────────┘
                                │ PUT /api/v1/tasks/:id
                                ▼
 ┌─────────────────────────────────────────────────────────────┐
 │                     internal/task 领域                      │
 │                                                             │
 │  ┌───────────────────────────────────────────────────────┐  │
 │  │ Store.SaveTaskWithinBudget                            │  │
 │  │  - 扫描线算法 (Sweep-line) 校验一周瞬时峰值 <= 1000 点 │  │
 │  │  - 持久化 schedule_json 到 task_algorithms 表         │  │
 │  └───────────────────────────┬───────────────────────────┘  │
 │                              │                              │
 │  ┌───────────────────────────┴───────────────────────────┐  │
 │  │ ScheduleWorker (后台时间状态轮询)                      │  │
 │  │  - 周期检测有效运行状态翻转                           │  │
 │  │  - 广播 TaskChangeListener.OnTaskChanged(algoID)      │  │
 │  └───────────────────────────┬───────────────────────────┘  │
 └──────────────────────────────┼──────────────────────────────┘
                                │ OnTaskChanged
                                ▼
 ┌─────────────────────────────────────────────────────────────┐
 │                  internal/algorithm 领域                    │
 │                                                             │
 │  Supervisor: 防抖重载流订阅 (SyncBrokerSubscriptions)        │
 │   - 当前在排程内: Attach AI Stream，拉起 Runner              │
 │   - 当前不在排程: Release AI Stream，Runner 待命 Standby     │
 └─────────────────────────────────────────────────────────────┘
```

---

## 2. 数据模型设计

### 2.1 数据库 Schema (`000025_add_schedule_json_to_task_algorithms.up.sql`)
```sql
ALTER TABLE task_algorithms ADD COLUMN schedule_json TEXT NOT NULL DEFAULT '{}';
```

### 2.2 Go 结构定义 (`internal/schedule` 独立包)
```go
package schedule

// TimeRange 表示一天内的时间区间（秒为单位或 HH:mm 格式，分钟级精度 0..1440）
type TimeRange struct {
    Start string `json:"start"` // "08:30"
    End   string `json:"end"`   // "17:30"
}

// WeekSchedule 包含一周 7 天的运行计划
type WeekSchedule struct {
    Enabled bool                   `json:"enabled"` // 为 false 时视为 24x7
    Days    map[string][]TimeRange `json:"days"`    // "monday", "tuesday", ..., "sunday"
}
```
- **空 JSON `{}` 语义**：`Enabled == false` 或空对象视为全天 24×7 运行。
- **归一化算法**：自动对同一天的区间进行排序并合并相邻/重叠的区间，拦截 `start >= end` 的非法输入。

---

## 3. 瞬时峰值算力算法 (Sweep-Line Peak Compute)

将一周 7 天展开为从 `0` 到 `7 × 1440 = 10080` 分钟的时间轴。
每个启用的算法子项贡献其点数 $C_i = \text{sample\_fps} \times \text{compute\_cost}$：
1. 若算法无计划（24×7 全开），则在整个 `[0, 10080)` 区间基础底噪加上 $C_i$；
2. 若算法有计划，对每个时间区间 $[S, E)$：
   - 产生离散事件：时刻 $S$ 产生 $+C_i$，时刻 $E$ 产生 $-C_i$；
3. 将所有离散事件按时间排序；
4. 顺序扫描时间事件，维护累加变量 `currentPoints`，并记录 $\text{peak} = \max(\text{peak}, \text{currentPoints})$；
5. 若 $\text{peak} > 1000$，拒绝事务并报错。

时间复杂度：$O(M \log M)$（其中 $M$ 为全系统所有算法的区间总数，通常 $< 200$），耗时在微秒级，在数据库事务内执行毫无压力。

---

## 4. 运行时调度联动 (ScheduleWorker)

- **Worker 职责**：在 `task.Service` 启动时拉起一个后台 goroutine（或 `time.Ticker`，默认 30 秒间隔）；
- **状态比对**：
  - 维护内存快照：上一次检查时各 `(taskID, algoID)` 的激活状态 `bool`；
  - 针对当前时刻 `t = time.Now().In(systemLocation)`，计算最新激活状态；
  - 识别出发生变化的 `algorithmID` 集合，调用 `listener.OnTaskChanged(ctx, algoID)`；
- **优雅停机**：遵从 context 取消与 Fx 停机生命周期。

---

## 5. 前端架构与交互 (React SPA)

1. **组件设计 (`ScheduleGrid`)**：
   - 纵轴：周一至周日（7 行）；
   - 横轴：00:00 至 24:00（48 列，每列 30 分钟）；
   - 交互：支持像 Excel 或海康 NVR 那样鼠标拖动批量刷取、反选、快捷按钮（全天、工作日、清除）；
2. **状态集成 (`use-channel-task-drafts.ts`)**：
   - `AlgoDraftItem` 增加 `scheduleJson: string`；
   - 动态算力计量（`ChannelTaskBudgetMeter`）：前端同样引入峰值计算，显示「峰值算力: X 点」和「当前算力: Y 点」，超限即时给出红色警示并禁用保存按钮。
