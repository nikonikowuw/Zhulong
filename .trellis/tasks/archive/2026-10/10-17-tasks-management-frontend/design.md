# Technical Design: 通道多算法分析任务管理 (Analysis Tasks Frontend)

## 1. 架构定位与多层拓扑

本设计在保持 Go 后端与原生引擎 `(camera_id, algorithm_id)` 极简规范的前提下，在前端 `web/src/features/tasks` 落地**「商业级通道多算法聚合」**架构：

```txt
┌────────────────────────────────────────────────────────────────────────┐
│                        React 19 SPA (TanStack Router)                  │
│                                                                        │
│   src/routes/_authenticated/tasks/index.tsx                            │
│                      │                                                 │
│   src/features/tasks/index.tsx (主页面容器)                              │
│         ├─ TasksComputeBudgetCard (1000点算力看板)                      │
│         ├─ TasksViewHeader (视图切换: 通道卡片矩阵 ⇋ 任务表格)             │
│         │     ├─ ChannelTasksView (旷视/大厂风: 通道卡片聚合多算法)       │
│         │     └─ TasksTable (运维排查风: 扁平任务明细表格)               │
│         ├─ ChannelTaskConfigDialog (通道多算法勾选、调参、算力预览弹窗)     │
│         └─ TaskDeleteDialog (删除确认弹窗)                              │
│                      │                                                 │
│   src/features/tasks/hooks/ (状态与派生 Hook)                           │
│         ├─ useTasks (基础任务查询)                                      │
│         ├─ useChannelTasks (核心派生: 按相机聚合多算法、计算通道级算力)     │
│         ├─ useSyncChannelAlgorithms (核心突变: 多算法差量批量同步)         │
│         └─ useTaskComputeBudget (全局算力消耗派生计算)                   │
│                      │                                                 │
│   src/features/tasks/api/tasks-api.ts (Zod 强类型 API Client)           │
└──────────────────────┬─────────────────────────────────────────────────┘
                       │ HTTP RESTful (GET / POST / PUT / DELETE /tasks)
                       ▼
┌────────────────────────────────────────────────────────────────────────┐
│                        Go 边缘宿主后端 (Uber Fx)                         │
│   /api/v1/tasks (保持底层轻量解耦与 Supervisor 按算法包单例运行)          │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 核心状态派生与数据流 (`useChannelTasks`)

为了不在客户端维护冗余可变状态，所有通道级聚合数据均在渲染期通过纯函数派生：

```typescript
export interface ChannelTaskItem {
  task: Task
  algorithm: Algorithm | undefined
  computePoints: number // algorithm.computeCost * task.sampleFps
}

export interface ChannelTaskGroup {
  camera: Camera
  items: ChannelTaskItem[]
  totalChannelPoints: number // 该通道内所有活跃算法的算力点数总和
  activeCount: number
  totalCount: number
}

export function useChannelTasks(
  cameras: Camera[],
  algorithms: Algorithm[],
  tasks: Task[]
): ChannelTaskGroup[] {
  return useMemo(() => {
    const algoMap = new Map(algorithms.map((a) => [a.id, a]))
    const tasksByCamera = new Map<string, Task[]>()

    for (const t of tasks) {
      const list = tasksByCamera.get(t.cameraId) ?? []
      list.push(t)
      tasksByCamera.set(t.cameraId, list)
    }

    return cameras.map((camera) => {
      const cameraTasks = tasksByCamera.get(camera.id) ?? []
      let totalChannelPoints = 0
      let activeCount = 0

      const items: ChannelTaskItem[] = cameraTasks.map((task) => {
        const algorithm = algoMap.get(task.algorithmId)
        const cost = algorithm?.computeCost ?? 10
        const points = cost * task.sampleFps
        if (task.enabled) {
          totalChannelPoints += points
          activeCount++
        }
        return { task, algorithm, computePoints: points }
      })

      return {
        camera,
        items,
        totalChannelPoints,
        activeCount,
        totalCount: items.length,
      }
    })
  }, [cameras, algorithms, tasks])
}
```

---

## 3. 差量批量同步策略 (`useSyncChannelAlgorithms`)

当用户在「通道多算法配置弹窗」中勾选/取消勾选算法，并点击“保存配置”时：

```typescript
export interface AlgorithmConfigDraft {
  algorithmId: string
  sampleFps: number
  enabled: boolean
  configJson: string
}

// 差量同步算法：
// 1. 新勾选 (draft 有, existing 无) -> POST /api/v1/tasks
// 2. 已修改 (draft 有, existing 有, 但 fps/enabled/config 发生变化) -> PUT /api/v1/tasks/:id
// 3. 取消勾选 (draft 无, existing 有) -> DELETE /api/v1/tasks/:id
```
- **原子事务与防雪崩**：先发起校验与算力预估，前端预先阻止超过 1,000 点的提交；
- 异步操作通过 `Promise.allSettled` 或受控并发执行，任何失败均会记录并弹出友好汇总 Toast，之后自动 `queryClient.invalidateQueries({ queryKey: ['tasks'] })` 刷新真实状态。

---

## 4. UI 交互与视觉表现

### 4.1 通道卡片聚合视图 (Channel View)
- 采用自适应响应式网格（`grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4`）；
- 每张卡片呈现：
  - 顶部：摄像头名称、在线状态圆点（`online`/`offline`）、推流分辨率；
  - 中部：已配置算法徽标列表：
    - 运行态：`border-teal-200 bg-teal-100/30 text-teal-900 dark:border-teal-800 dark:bg-teal-950/30 dark:text-teal-200` 带绿点；
    - 悬浮展开展示该算法的采样率与点数（如 `10 FPS · 100点`）；
  - 底部：本通道总算力消耗指示条 + 「+ 配置算法」按钮。

### 4.2 任务扁平表格视图 (Flat Table View)
- 适合精细化运维与快速排序，支持行内 Switch 独立启停。

### 4.3 通道配置弹窗 (Channel Task Config Dialog)
- 顶部显示所选摄像头信息与总算力预算指示条；
- 中间展示算法选择列表，每个算法带有 Checkbox、FPS 滑块（仅在勾选时展开）、以及该算法的预估算力徽标；
- 底部实时汇总：`本通道算力: X 点 | 全平台占用: Y / 1000 点`。

---

## 5. 错误处理与降级守卫

1. **算力预算超限 (409 COMPUTE_BUDGET_EXCEEDED)**：
   - 弹窗内置客户端前置计算阻断，避免无效的网络往返；
   - 捕获后端 409 异常，提供清晰文案引导用户降低 FPS 或关停其他通道算法。
2. **多语言与 RTL**：
   - 严格使用逻辑属性，中繁英全量适配。
