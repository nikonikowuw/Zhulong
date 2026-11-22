# Technical Design: Optimize tasks ChannelTaskDialog UI & calibration UX

## 1. Architecture & Component Boundaries

本次改造完全局限于前端 `web/src/features/tasks/` 内部组件、Hook 与本地测试，不改变后端 API 契约（`/tasks`、`/tasks/camera/:cameraId`）及跨特性契约（`features/live`、`features/cameras`、`features/algorithms`）。

涉及模块与边界：
- **布局与交互壳层**：`channel-task-dialog.tsx`、`channel-task-dialog-header.tsx`、`channel-task-budget-meter.tsx`、`channel-task-region-viewport.tsx`、`channel-task-algo-panel.tsx`
- **动态表单模式联动**：`use-algo-config-form.ts`
- **画布事件层**：`region-canvas.tsx`
- **编辑器状态容器与草稿编排**：`use-region-editor.ts`、`use-channel-task-drafts.ts`

## 2. Detailed Design per Requirement

### 2.1 R1: 弹窗窄屏响应式布局

1. **`ChannelTaskDialogContent`** (`channel-task-dialog.tsx`):
   - 主工作区容器由：
     `flex min-h-0 flex-1 divide-x divide-border overflow-hidden bg-muted/20`
     调整为：
     `flex min-h-0 flex-1 flex-col divide-y divide-border overflow-y-auto bg-muted/20 lg:flex-row lg:divide-x lg:divide-y-0 lg:overflow-hidden`
   - 效果：`< lg` 视口下垂直堆叠，整体内容区允许上下滚动；`lg+` 恢复独立滚动双栏。

2. **`ChannelTaskRegionViewport`** (`channel-task-region-viewport.tsx`):
   - 根容器在 `< lg` 时给予确定视口高度，防止在 `flex-col` 可滚动容器内塌陷或过度拉长：
     `flex h-[42vh] min-h-[260px] w-full shrink-0 flex-col overflow-hidden bg-zinc-950 lg:h-auto lg:min-h-0 lg:min-w-0 lg:flex-1`
   - 工具栏 (`region-editor-toolbar.tsx`) 已具备 `flex-wrap`，工具组内按钮可在窄屏下补 `flex-wrap`（`flex flex-wrap items-center ...`）避免单个白框工具条撑破宽度。

3. **`ChannelTaskAlgoPanel`** (`channel-task-algo-panel.tsx`):
   - 面板容器由 `flex w-[460px] shrink-0 flex-col ... xl:w-[490px]` 调整为：
     `flex w-full shrink-0 flex-col gap-3.5 bg-muted/30 p-4 lg:w-[440px] lg:overflow-y-auto xl:w-[490px]`
   - `< lg` 时面板自然展开随外层工作区一起滚动，避免嵌套双滚动条；`lg+` 时独立滚动。

4. **`ChannelTaskDialogHeader` & `ChannelTaskBudgetMeter`**:
   - Header 顶层容器增加 `flex-wrap gap-x-4 gap-y-2.5`，右侧开关区增加 `flex-wrap`。
   - `ChannelTaskBudgetMeter` 移除 `hidden lg:flex`，改为 `flex w-full flex-col gap-1 lg:w-auto`（在 Header 中通过 `order-3 lg:order-none` 让窄屏下算力条自然排成第二行全宽条，进度条在 `< lg` 使用 `w-full lg:w-52`），保证移动/平板视口仍能即时感知算力占用与超限告警。

### 2.2 R2: ROI / MASK 重叠绘制防抢占

- 在 `region-canvas.tsx` 中：
  - 定义 `const rectInteractive = !disabled && tool === 'select'`。
  - 每个 ROI/MASK 矩形容器在 `!rectInteractive` 时：
    - 加上 `pointer-events-none`；
    - 鼠标指针样式仅在 `rectInteractive` 时为 `cursor-move`；
    - 四角缩放手柄仅在 `isSelected && rectInteractive` 时渲染。
  - `renderCrop` 维持现有逻辑：`cropEditable = tool === 'crop' && !disabled`，仅在 `crop` 模式下可拖动/缩放 `CROP` 框，其余模式均为 `pointer-events-none`。
  - `TripwireCanvas` 维持现有逻辑：仅在 `tool === 'tripwire'` 时捕捉全画布绘制，`tool === 'select'` 时仅端点圆圈接收指针。
- 数据流验证：在 `tool === 'roi'` 或 `'mask'` 时，点击已有矩形内部会直接穿透到画布根节点 `handleBackgroundMouseDown`，正常触发 `creating` 状态机并在抬起后回调 `onCreate`；切回 `select` 时恢复矩形点选、平移和缩放。

### 2.3 R3: 保存参数校验失败自动切回参数 Tab

1. **受控 Tab 状态**：
   - 导出类型 `export type ChannelTaskAlgoTab = 'params' | 'schedule'`（或由 `ChannelTaskAlgoPanelProps` 接受可选受控属性 `activeTab?: ChannelTaskAlgoTab` / `onTabChange?: (tab: ChannelTaskAlgoTab) => void`，保留默认回退以兼容独立渲染/测试）。
   - `ChannelTaskDialogContent` 持有 `const [activeTab, setActiveTab] = useState<'params' | 'schedule'>('params')`。
   - `handleSave` 中 `validateSelectedAlgoDrafts` 返回非空 `issue` 时，同时调用 `selectActiveAlgo(issue.algoId)` 和 `setActiveTab('params')`。
2. **非法 JSON 自动切至源码视图**：
   - `useAlgoConfigForm` 中 `mode` 初始态改为 `() => (safeParseJson(value) === null ? 'json' : 'form')`。
   - 外部 `value` 变更同步 effect（L96-106）中：若 `safeParseJson(value) === null` 且当前为 `'form'`，通过渲染期派生或在现有 `value` 变更处理中确保呈现 `'json'` 编辑器；注意遵循 `.trellis/spec/frontend/development-guidelines.md` §1.9（尽量避免 `useEffect` 联动 `setState`，可通过 `ChannelTaskAlgoPanel` 给 `AlgoConfigDynamicForm` 传 `key` 或在 `useAlgoConfigForm` 内用 render-time synced state / `switchMode`）。
   - 实际上因为 `ChannelTaskAlgoPanel` 已使用 `key={activeAlgo.id}` 挂载 `AlgoConfigDynamicForm`，且在同算法下处于 `'schedule'` Tab 时 `TabsContent value='params'` 处于卸载状态，切回 `'params'` Tab 时 `AlgoConfigDynamicForm` 会重新挂载并立即根据 `safeParseJson(value) === null` 初始化为 `'json'` 模式！
   - 若用户在同一算法下没有卸载（例如在 `forceMount` 不存在的普通 Radix Tabs 中从 `schedule` 切回 `params` 原本就会重新挂载；若未来从外部重置为非法 JSON，也可在渲染期检测 `value !== syncedValue && safeParseJson(value) === null` 时切为 `'json'`）。

### 2.4 R4: 键盘快捷键（Undo / Delete）

- 提取干净的纯辅助函数（如 `isEditableEventTarget(target: EventTarget | null): boolean`）：
  - 判断目标或其最近祖先是否属于 `INPUT`, `TEXTAREA`, `SELECT`, `[contenteditable='true']`, `[contenteditable='']`, `[role='textbox']`, `[role='combobox']`, `[role='listbox']`, `[role='menu']`, `[role='slider']`, `[role='spinbutton']`。
- 在 `ChannelTaskDialogContent` 中挂载 `window` 级 `keydown` 监听（`ChannelTaskDialog` 仅在 `open` 时渲染 `ChannelTaskDialogContent`，因此挂载/卸载自动与弹窗开关同步）：
  - 跳过 `event.defaultPrevented || event.isComposing` 及 `isEditableEventTarget(event.target)`。
  - **撤销**：`(event.ctrlKey || event.metaKey) && !event.altKey && !event.shiftKey && event.key.toLowerCase() === 'z'`
    - 条件：`(canEditRegion || canEditCrop) && regionEditor.canUndo`
    - 动作：`event.preventDefault(); regionEditor.undo()`
  - **删除**：`!event.ctrlKey && !event.metaKey && !event.altKey && (event.key === 'Delete' || event.key === 'Backspace')`
    - 若 `canEditRegion && regionEditor.selected !== null`：`event.preventDefault(); regionEditor.deleteRect(regionEditor.selected.category, regionEditor.selected.index)`
    - 否则若 `canEditCrop && regionEditor.tool === 'crop' && regionEditor.crop !== null`：`event.preventDefault(); regionEditor.clearCrop()`

### 2.5 R5: Crop 撤销栈与跨算法历史保护

重新设计 `useRegionEditor` 内部历史栈结构，同时保持对 `RegionEditorController` 现有消费者的向后兼容：

```ts
type EditorHistoryEntry =
  | { kind: 'region'; scopeKey: string; draft: RegionDraft }
  | { kind: 'crop'; crop: Rect | null }
```

- **作用域键 (`scopeKey`)**：
  - `useRegionEditor(initialValue, initialCrop, initialScopeKey = 'default')` 新增可选第 3 参数 `initialScopeKey`；`reset(value, nextScopeKey?)` 新增可选第 2 参数 `nextScopeKey`。
  - `useChannelTaskDrafts` 在初始化时传入 `state.activeAlgoId ?? '__none__'`，并在 `selectActiveAlgo` / `toggleAlgo` / `changeCamera` 调用 `reset(nextJson, nextAlgoId ?? '__none__')`。
- **快照采集与分类提交**：
  - `snapshot()` 记录当前 `pendingSnapshotRef.current = { scopeKey: scopeKeyRef.current, draft: draftRef.current, crop: cropRef.current }`（不立即写入 `history`，避免空操作或重复拖拽中间态）。
  - 当随后调用 `setCrop(rect)` 时，若存在 `pendingSnapshotRef`，将其以 `{ kind: 'crop', crop: pending.crop }` 压入 `history` 并清空 `pendingSnapshotRef`；`clearCrop()` 直接将 `{ kind: 'crop', crop: cropRef.current }` 压入 `history`。
  - 当随后调用 `changeDraft(next)` / `createRect(...)` 时，若存在 `pendingSnapshotRef`，将其以 `{ kind: 'region', scopeKey: pending.scopeKey, draft: pending.draft }` 压入 `history` 并清空 `pendingSnapshotRef`；`deleteRect(...)` 直接将 `{ kind: 'region', scopeKey: scopeKeyRef.current, draft: draftRef.current }` 压入 `history`。
  - 每次压栈截断至 `MAX_HISTORY`（50 条）。
- **`reset` 与 `resetCrop` 语义**：
  - `reset(value, nextScopeKey)`: 更新 `draft`、`baseline`、`selected = null`、`tool = 'select'`，并清空 `pendingSnapshotRef`；
    - 当传入 `nextScopeKey`（跨算法切换模式）时：**不清空 `history`**，仅切换 `scopeKey`；
    - 当省略 `nextScopeKey`（兼容独立单算法重置调用）时：仅移除当前默认作用域的 `'region'` 历史，仍保留 `'crop'` 历史。
  - `resetCrop(value)`: 切换摄像头时调用，更新 `crop`、`cropBaseline`、`selected = null`、`pendingSnapshotRef = null`，并清空全部 `history`（`setHistory([])`）。
- **`canUndo` 与 `undo()`**：
  - 辅助函数 `findLatestUndoIndex(history, scopeKey)`：从栈尾向前查找第一条满足 `entry.kind === 'crop' || entry.scopeKey === scopeKey` 的索引。
  - `canUndo = findLatestUndoIndex(history, scopeKey) !== -1`。
  - `undo()`：
    - 查找最新匹配项；若无则直接返回。
    - 若为 `'crop'`：仅恢复 `setCropState(entry.crop)`，不动当前算法 `draft`。
    - 若为 `'region'`：仅恢复 `setDraft(entry.draft)`，不动当前通道 `crop`（并清除 `selected`）。
    - 从 `history` 中移除该索引对应条目。

## 3. Risks, Trade-offs & Compatibility

- **兼容性**：`RegionEditorController` 接口及 `useRegionEditor(initialValue, initialCrop)` 双参签名完全向后兼容，现有 `tripwire-canvas.test.tsx` 与 `region-canvas.test.tsx` 无需改动即可运行。
- **拖拽防抖与快照边界**：`pendingSnapshotRef` 仅在同步事件回调（`snapshot()` 后紧接 `setCrop` / `changeDraft` / `createRect`）消费；在 `reset` / `resetCrop` / `undo` 时主动清零 `pendingSnapshotRef`，防止跨状态残留。
- **快捷键防误触**：对于 Radix UI 组件（如 `Select` 触发器或 `Checkbox`），保留 `Delete`/`Backspace` 仅针对选中区域与 `crop` 生效，且当焦点在任何表单输入、文本域、滑块或弹出菜单内时静默跳过。
