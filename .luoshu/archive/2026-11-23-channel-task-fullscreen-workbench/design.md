# Design: 2026-11-23-channel-task-fullscreen-workbench

## 架构与实现方案

### 1. 容器重构策略：从 Radix `Dialog` 到独立全屏工作台

当前 `ChannelTaskDialog` 使用 `<Dialog>` + `<DialogContent className="h-[94vh] max-h-[1000px] w-[98vw] ...">`，导致存在 2vw/6vh 的外围黑边遮罩与弹窗边框，且被 Radix `Dialog` 的居中定位与尺寸上限（`max-h-[1000px]` / `2xl:max-w-[1680px]`）限制。

重构后采用**全屏工作台覆盖层（Fullscreen Workbench Overlay）**架构：
- **Portal 挂载**：通过 `react-dom` 的 `createPortal(..., document.body)` 挂载到文档根节点，避免受父级容器 `overflow` 或 `z-index` 层叠上下文影响。
- **全屏视口布局**：
  ```tsx
  <div
    role='dialog'
    aria-modal='true'
    aria-labelledby={WORKBENCH_TITLE_ID}
    aria-describedby={WORKBENCH_DESC_ID}
    className='fixed inset-0 z-50 flex h-dvh w-screen flex-col overflow-hidden bg-background text-foreground'
  >
  ```
  - 彻底移除 `max-h-[1000px]`、`w-[98vw]`、`2xl:max-w-[1680px]`、圆角及居中偏移，确保任何分辨率下都占满 `100vw × 100dvh`。
- **底层页面滚动锁定**：
  - 打开期间在 `useEffect` 中记录并设置 `document.body.style.overflow = 'hidden'`，关闭或卸载时精准恢复原值，避免底层任务列表在滚轮操作时意外滚动。
- **键盘 `Escape` 关闭与快捷键隔离**：
  - 保留原有 `isEditableEventTarget(e.target)` 判断；
  - 若当前未处于文本输入态且非保存中（`!isSaving`），按 `Escape` 触发 `onClose()`（与原 Dialog 行为一致，同时避免与内部输入组件冲突）。

### 2. 子组件与 Radix Dialog 上下文解耦

Radix UI 的 `DialogTitle` / `DialogDescription` 强依赖 `<Dialog>` Provider 上下文，脱离 `<Dialog>` 后会抛出运行时错误。因此同步重构两个子组件：

1. **`channel-task-dialog-header.tsx`**：
   - 将 `<DialogHeader>` 替换为 `<header className='shrink-0 border-b border-border bg-card px-4 py-3 text-start sm:px-6 sm:py-3.5'>`；
   - 将 `<DialogTitle>` 替换为 `<h2 id={WORKBENCH_TITLE_ID} ...>`；
   - 将 `<DialogDescription>` 替换为 `<p id={WORKBENCH_DESC_ID} className='sr-only'>`；
   - 保留所有相机切换器、算力预算进度条、通道启用开关、禁用算法自启开关与右上角关闭按钮。

2. **`channel-task-dialog-footer.tsx`**：
   - 将 `<DialogFooter>` 替换为 `<footer className='flex shrink-0 items-center justify-end gap-2 border-t border-border bg-card px-6 py-3'>`；
   - 保持“取消”与“保存并下发调度”按钮及禁用/保存中状态不变。

### 3. 主工作区与滚动边界

- 主工作区容器保持 `flex min-h-0 flex-1 flex-col divide-y divide-border overflow-y-auto bg-muted/20 lg:flex-row lg:divide-x lg:divide-y-0 lg:overflow-hidden`：
  - 桌面端（`lg+`）：工作台整体锁定 `h-dvh`，无外层滚动条；左侧 `ChannelTaskRegionViewport` 自适应占满剩余高度，右侧 `ChannelTaskAlgoPanel` 内部独立垂直滚动。
  - 移动端/窄屏（`< lg`）：主工作区单列垂直滚动，Header 与 Footer 始终分别吸附于顶部与底部。

### 4. 向后兼容

- `channel-task-dialog.tsx` 继续导出 `ChannelTaskDialog` 和 `ChannelTaskDialogProps`，并可追加导出别名 `ChannelTaskWorkbench`，`tasks-dialogs.tsx` 无需改动调用方式即可无缝升级。

## 外部技术调研与参考

- **React 19 `createPortal` (`react-dom`)**：标准挂载方案，兼容 Vitest 真实 Chromium 浏览器测试中的 `page.getByRole(...)` 查询（Portal 内容位于 `document.body` 下，与 Radix Portal 行为一致）。
- **CSS `h-dvh`（Dynamic Viewport Height）**：相比 `100vh`，在平板或浏览器工具栏动态伸缩时更准确，避免底部 Footer 被遮挡。

## 风险与审查重点（Review Focus）

1. **Radix Context 残留崩溃风险**：
   - 必须确保 `channel-task-dialog.tsx`、`channel-task-dialog-header.tsx`、`channel-task-dialog-footer.tsx` 中已完全移除 `@/components/ui/dialog` 的任何残余组件引用，否则脱离 `<Dialog>` 根组件渲染会立即报错。
2. **Body `overflow: hidden` 清理泄漏**：
   - 当工作台关闭或组件意外卸载时，`useEffect` cleanup 必须将 `document.body.style.overflow` 恢复到打开前的原始值，不能破坏任务列表页面的正常滚动。
3. **保存期间的关闭防护与 `Escape` 快捷键**：
   - 正在下发同步（`isSaving === true`）或焦点位于 `input`/`textarea`/`select` 时，确保快捷键与关闭行为符合预期，不发生误关或竞态。
4. **全屏布局与无外层滚动条验证**：
   - 在真实 Chromium 浏览器测试中验证工作台根元素覆盖整个视口（`width === innerWidth`、`height === innerHeight`），且不存在居中弹窗的暗色外边距或横向溢出。
