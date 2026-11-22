# Optimize tasks ChannelTaskDialog UI & calibration UX

## Goal

提升 `web/src/features/tasks` 中通道算法配置工作台（`ChannelTaskDialog`）在窄屏视口下的可用性、复杂空间区域叠加绘制体验、表单校验错误定位能力与标定撤销/删除操作效率，同时确保任务级裁剪窗口（Crop）与算法级区域（ROI / MASK / 绊线）之间的撤销历史互不破坏。

## Background & Confirmed Facts

1. **窄屏布局挤压与算力条丢失**：
   - `web/src/features/tasks/components/channel-task-dialog.tsx#L330` 主工作区固定为横向 `flex divide-x`，无断点垂直堆叠策略。
   - `web/src/features/tasks/components/channel-task-algo-panel.tsx#L79` 固定右侧宽度 `w-[460px] xl:w-[490px] shrink-0`，在 `< 1024px` 视口会严重挤压左侧实时视频标定视口。
   - `web/src/features/tasks/components/channel-task-budget-meter.tsx#L41` 使用 `hidden lg:flex`，`< 1024px` 时顶部全平台/本通道算力预算完全不可见。
2. **ROI / MASK 绘制模式被已有矩形抢占事件**：
   - `web/src/features/tasks/components/region-canvas.tsx#L120-L128` 与 `web/src/features/tasks/components/use-region-drag.ts#L207` 在所有工具下都让已有 ROI/MASK 矩形接收 `onMouseDown` 并调用 `e.stopPropagation()`。
   - 在 `+ ROI` 或 `+ MASK` 绘制模式下，若起始点落在既有矩形内部（例如在 ROI 内绘制 MASK 或绘制重叠 ROI），会误触发旧矩形平移而不是新建矩形。
3. **保存校验失败后 Tab 未同步切换**：
   - `web/src/features/tasks/components/channel-task-algo-panel.tsx#L205` 使用非受控 `<Tabs defaultValue='params'>`。
   - `web/src/features/tasks/components/channel-task-dialog.tsx#L191-L223` 在校验失败时仅调用 `selectActiveAlgo(issue.algoId)`；若用户停留在 `schedule`（分时布防计划）选项卡，出错表单仍被隐藏。
   - 此外，`web/src/features/tasks/hooks/use-algo-config-form.ts#L57` 默认以 `'form'` 模式挂载，当目标算法保存有非法 JSON 字符串时不会自动进入 `'json'` 模式暴露错误文本。
4. **缺少标定快捷键且切换算法会清空 Crop 撤销历史**：
   - `web/src/features/tasks/hooks/use-region-editor.ts#L112-L122` 中 `reset(value)` 在切算法时调用 `setHistory([])`，会清空同一通道下此前编辑任务级 `crop` 产生的撤销历史，也会抹除切回原算法后的区域撤销栈。
   - 画布与弹窗当前仅支持鼠标点击工具栏「撤销」与列表删除按钮，缺少 `Ctrl/Cmd+Z` 撤销和 `Delete/Backspace` 删除选中区域的键盘快捷键。

## Requirements

- **R1 · 弹窗窄屏响应式布局 (`ChannelTaskDialog` / `Header` / `BudgetMeter` / `RegionViewport` / `AlgoPanel`)**
  - `< lg`（`< 1024px`）视口下，工作区切换为上下垂直排列：上方展示实时画面与标定工具条（具合理高度与最小高度约束，避免塌陷或挤压），下方展示全宽算法矩阵与配置面板，垂直分隔线切换为水平分隔线。
  - `lg+`（`>= 1024px`）视口保持既有左画面弹性铺满 + 右检视器固定侧栏（`440px` / `490px`）的工作台布局。
  - 顶栏控制条（`ChannelTaskDialogHeader`）允许合理换行，`ChannelTaskBudgetMeter` 在各断点保持可见，避免窄屏丢失算力超载/余量感知。

- **R2 · ROI / MASK 重叠绘制防事件抢占 (`RegionCanvas`)**
  - 仅在 `tool === 'select'` 时允许选中、平移与缩放既有 ROI/MASK 矩形（含缩放手柄显示）。
  - 在 `tool === 'roi' | 'mask' | 'crop'` 绘制模式下，既有 ROI/MASK 矩形及其手柄禁用指针事件（`pointer-events-none`），允许从任意位置（包含既有矩形内部）直接起始拖拽创建新矩形；`crop` 工具仍保留对既有 `CROP` 框本身的拖拽/缩放及背景重绘能力。

- **R3 · 保存参数校验失败自动定位至参数 Tab (`ChannelTaskDialog` / `ChannelTaskAlgoPanel` / `useAlgoConfigForm`)**
  - 将 `ChannelTaskAlgoPanel` 的 `params` / `schedule` 选项卡受控化；`handleSave` 校验 `validateSelectedAlgoDrafts` 命中错误时，同步切换至 `issue.algoId` 与 `'params'` 选项卡。
  - 当算法配置文本为非法 JSON（`safeParseJson(value) === null`）时，`useAlgoConfigForm` 初始或同步切换到 `'json'` 模式，确保语法错误详情和源码可直接展示。

- **R4 · 区域编辑快捷键支持 (`ChannelTaskDialog` / `ChannelTaskRegionViewport`)**
  - 在工作台弹窗打开且焦点不在文本/数值/下拉/滑块/菜单等输入控件内、且未处于 IME 组合输入态时：
    - `Ctrl+Z` / `Cmd+Z`（非 Shift）：当 `(canEditRegion || canEditCrop) && regionEditor.canUndo` 时执行 `regionEditor.undo()`。
    - `Delete` / `Backspace`：当 `canEditRegion && regionEditor.selected !== null` 时删除当前选中 ROI/MASK 矩形；否则当 `canEditCrop && regionEditor.tool === 'crop' && regionEditor.crop !== null` 时清空当前 `crop`。

- **R5 · Crop 撤销栈与跨算法历史保护 (`useRegionEditor` / `useChannelTaskDrafts`)**
  - 按操作对象区分撤销快照（`region` 快照绑定当前算法 `scopeKey`，`crop` 快照属于通道级）；保持单一有序历史时间线以保证同算法内区域与 Crop 仍按操作倒序依次撤销。
  - 切换算法（`selectActiveAlgo` / `toggleAlgo`）不再清空 `history`：切到新算法后 `crop` 变更仍可撤销，切回原算法后原算法的区域变更也仍可撤销。
  - 切换摄像头（`changeCamera` -> `resetCrop`）时重置撤销历史，避免跨摄像头串扰。

## Acceptance Criteria

- [ ] **AC1（窄屏布局）**：在 `< 1024px` 视口下，`ChannelTaskDialog` 呈上下分栏且不发生水平溢出，顶部 `ChannelTaskBudgetMeter` 可见，视频画布维持可读高度不被右侧面板挤瘪；`>= 1024px` 维持左右双栏工作台布局。
- [ ] **AC2（重叠绘制）**：当画布上已有 ROI/MASK 矩形时，切换至 `+ ROI 关注区` 或 `+ MASK 屏蔽区` 工具并在旧矩形内部拖拽，能够正常创建新的 ROI/MASK 矩形而不拖动旧矩形；切回 `选择 / 调整` 后仍可正常选中、平移和缩放旧矩形。
- [ ] **AC3（校验切 Tab）**：当停留在「分时布防计划」选项卡点击「保存并下发调度」，若任意已选算法存在 Schema 越界或非法 JSON，保存被阻断、自动聚焦到首个出错算法并切回「运行参数与区域」选项卡（非法 JSON 场景直接展示 JSON 源码与报错）。
- [ ] **AC4（快捷键与输入隔离）**：在画布选中矩形后按 `Delete` / `Backspace` 可删除该矩形；按 `Ctrl+Z` / `Cmd+Z` 可撤销上一步标定/裁剪；当焦点位于输入框、文本框（如 JSON 编辑器 / FPS / 绊线名称 / 时间输入框）或下拉框内时，快捷键不拦截正常输入与编辑。
- [ ] **AC5（Crop 撤销保护）**：在算法 A 下修改 `crop`（或区域）后切换到算法 B，`canUndo` 仍反映可撤销的 `crop` 操作且撤销仅回退 `crop` 而不破坏算法 B 区域草稿；切回算法 A 后其先前区域编辑仍可撤销；切换摄像头会清空撤销栈。
- [ ] **AC6（质量门禁）**：遵循项目前端规范（OKLCH 语义色、RTL 逻辑属性、零 `any`、三语完整、真实浏览器 Vitest 测试），受影响测试与 `make frontend-check` 全部通过。

## Out of Scope

- 列表主表/子表的列展示扩展（如子表增加绊线/录像/排程列、筛选口径重构）
- 弹窗内新增单算法子项启停开关、清空全部算法保存二次确认弹窗
- 路由冗余 `view` 参数与历史国际化键清理
- 任何 Go 后端接口或数据库 Schema 变更
