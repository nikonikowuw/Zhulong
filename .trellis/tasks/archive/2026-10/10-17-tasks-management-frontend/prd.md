# PRD: 开发分析任务管理前端模块 (Analysis Tasks Frontend)

## Goal

在 `web/src/features/tasks` 构建对齐旷视、英特灵达等成熟商业边缘计算产品的**「通道多算法分析任务管理」**模块。通过前端聚合抽象，向用户呈现以「摄像头/通道」为核心的多算法矩阵配置与调度视图，同时与后端 `/api/v1/tasks` 保持高效轻量同步，实现全平台 1,000 点算力预算可视化监控、通道级多算法批量配置、行内实时启停与路由导航集成。

## Requirements

### R1. 核心模型与 API 映射 (Data Schema & Mapping)
- 遵循后端 `/api/v1/tasks` 原生 RESTful 契约（`GET`, `POST`, `PUT`, `DELETE`），使用 Zod 进行 100% 运行时类型校验。
- 业务层映射：
  - **物理层 (Backend)**：`Task` 为 `(cameraId, algorithmId)` 的独立调度实体。
  - **体验层 (Frontend Commercial UX)**：将同一个 `cameraId` 下的所有活跃算法任务，聚合成一个**「通道分析任务 (Channel Analysis Task)」**。

### R2. 双视角视图切换 (Dual-View Presentation)
- **通道聚合视图（Channel View - 默认商业视角）**：
  - 以摄像头/点位为卡片展示（如「📷 01-东门全景球机」）；
  - 卡片内展示该通道已配置的全部算法徽章（包含运行状态、采样 FPS、算力消耗）；
  - 提供单通道「配置算法」、「一键全启/全停」快捷操作。
- **任务扁平视图（Flat Table View - 工程师排查视角）**：
  - 传统数据表格，清晰罗列每条具体绑定的详细指标（任务 ID、相机名、算法名、FPS、独立开关、更新时间、操作）。
- 视图状态与检索条件与 URL 参数持久化同步（`useTableUrlState`）。

### R3. 通道多算法统一配置弹窗 (Channel Multi-Algorithm Dialog)
- 对齐商业大厂交互心智：
  1. **选择目标通道 (Camera)**：下拉选择摄像头，展示在线状态与推流信息（若在卡片中点击“配置算法”，则自动选中且只读锁定）；
  2. **多算法矩阵选择 (Multi-Algorithm Selection)**：
     - 展示所有已安装的算法包列表；
     - 提供复选框（Checkbox）供用户自由多选勾选；
  3. **单算法参数内联调整**：
     - 针对勾选的算法，展开其专属配置项：采样帧率（滑块 1~30 FPS）、算法参数 JSON（Schema 校验）；
  4. **全通道即时算力计算器 (Live Budget Calculator)**：
     - 实时联动计算当前摄像头所勾选算法的总算力点数（$\sum \text{cost}_i \times \text{fps}_i$）；
     - 联动展示配置后全局 1,000 点算力池的占用百分比，超限（$>1000$ 点）直接变红预警并拦截保存；
  5. **差量原子同步 (Diff Synchronization)**：
     - 保存时，前端自动比对该摄像头原有算法与新勾选算法：
       - 新增勾选的算法：调用 `POST /api/v1/tasks`；
       - 修改了 FPS/配置的算法：调用 `PUT /api/v1/tasks/:id`；
       - 取消勾选的算法：调用 `DELETE /api/v1/tasks/:id`；
     - 处理原子回滚与 Toast 提示，若中途出现 409 算力超限，捕获并提示。

### R4. 全平台算力预算看板 (Compute Budget Bar)
- 全平台总算力上限基准为 1,000 点；
- 统计所有启用任务（`enabled: true`）的动态消耗；
- 状态指示：绿色充裕（$\le 80\%$）、黄色警戒（$80\%\sim 100\%$）、红色超载（$>100\%$）。

### R5. 单任务与通道快速启停 (Inline Toggle)
- 通道视图下支持快速切换单算法或通道全局启停；
- 表格视图下支持行内 Switch 启停；
- 异常（409 超限）时自动回弹。

### R6. 规范、多语言与测试 (Spec, i18n & Tests)
- 100% 遵从 `satnaing/shadcn-admin` 视觉设计语言，状态 Badge 采用 Dot-Indicator Outline 模式并成对适配深浅色；
- 支持中/繁/英三语字典，支持 RTL 布局逻辑属性；
- 编写覆盖 API、算力派生、通道聚合 diff 同步的 Vitest 测试用例。

## Acceptance Criteria

- [x] AC1: `web/src/features/tasks/api/` 完整接入 `/api/v1/tasks`，单测 100% 通过。
- [x] AC2: 实现 `useChannelTasks` 派生 Hook，正确按摄像头聚合已绑定的算法任务。
- [x] AC3: 提供「通道聚合视图」与「扁平表格视图」，支持无缝切换与 URL 状态同步。
- [x] AC4: 顶部算力预算条实时准确反映全局已用点数与负载比例。
- [x] AC5: 「通道多算法配置弹窗」支持多选算法包、各自设置 FPS、即时预估总算力、并执行原子差量同步（POST/PUT/DELETE）。
- [x] AC6: 遇到 409 算力超限或重复任务时，给出精准友好的业务提示。
- [x] AC7: 路由 `/tasks` 与侧边栏导航正常挂载，中繁英三语全覆盖。
- [x] AC8: `pnpm test` 和 `pnpm run build` 全部无报错通过。
