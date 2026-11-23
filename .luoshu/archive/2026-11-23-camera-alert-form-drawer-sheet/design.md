# Design: 2026-11-23-camera-alert-form-drawer-sheet

## 架构与实现方案

### 1. 容器选型与组件替换原理

当前前端基础 UI 库中，`Dialog` 与 `Sheet` 均基于 `@radix-ui/react-dialog` 封装：
- `Dialog` 采用屏幕绝对居中布局：`fixed left-[50%] top-[50%] -translate-x-1/2 -translate-y-1/2`。
- `Sheet` 采用贴边滑动布局：`fixed inset-y-0 right-0 z-50`，自带自右向左平滑过渡动画（`data-[state=open]:slide-in-from-right`）。

两者的底层 API（`open`、`onOpenChange`、`Root`、`Portal`、`Overlay`、`Title`、`Description`）完全同构。因此改造无需重构表单逻辑，只需将外层语义容器从 `Dialog*` 切换为 `Sheet*`，并重构外壳布局。

### 2. 布局结构重构（Cameras 与 Alert Push 对齐）

#### A. 摄像头抽屉布局 (`cameras-action-dialog.tsx`)
- **根容器**：替换为 `<Sheet open={isOpen} onOpenChange={...}>`。
- **内容区**：`<SheetContent side="right" className="flex h-full w-full flex-col p-0 sm:max-w-xl md:max-w-2xl">`。
- **头部**：`<SheetHeader className="border-b px-6 py-4">`，展示操作图标、标题（新增摄像机 / 编辑摄像机）及副标题说明，右上方保留原生 `<SheetClose>` 按钮。
- **表单滚动区**：`<div className="flex-1 overflow-y-auto px-6 py-4">`，包裹 `CamerasActionStreamFields` 与基础信息，支持长内容垂直独立滚动，避免撑破页面。
- **底部吸附操作栏**：`<SheetFooter className="border-t bg-muted/20 px-6 py-3.5 sm:justify-between">`：
  - 左侧：探测中状态指示器（`Loader2` + 探测提示文案）；
  - 右侧：取消按钮（`variant="outline"`）与主操作提交按钮（关联 `form="camera-action-form"`）。

#### B. 告警推送抽屉布局 (`alert-destination-dialog.tsx`)
- **根容器**：替换为 `<Sheet open={open} onOpenChange={onOpenChange}>`。
- **内容区**：`<SheetContent side="right" className="flex h-full w-full flex-col p-0 sm:max-w-xl md:max-w-2xl">`。
- **头部**：`<SheetHeader className="border-b px-6 py-4">`，通道图标展示 + `SheetTitle` + `SheetDescription`。
- **主体区**：`<div className="flex-1 overflow-y-auto px-6 py-4">`，承载 `DestinationForm`，包含通道类型卡片选择、Webhook Headers 键值对表格、过滤规则开关等。
- **底部操作栏**：统一移至固定底部吸附栏，表单内容滚动时操作按钮保持常驻可见。

### 3. 向后兼容与导出规范

为避免破坏现有调用点（如 `cameras-dialogs.tsx` 与 `alert-push-page.tsx`）：
- 组件保留原有主导出名 `CamerasActionDialog` 与 `AlertDestinationDialog`，同时提供语义化别名 `CamerasActionDrawer` / `AlertDestinationDrawer`。
- 对外 Props 接口契约保持 100% 兼容。

## 外部技术调研与参考

- **Radix UI Dialog Primitive (v1.1+)**:
  - `@radix-ui/react-dialog` 的 `SheetContent` 会自动接管 ARIA 属性（`role="dialog"`、`aria-labelledby`、`aria-describedby`）与焦点捕获（Focus Trap）。
  - 在移动端或小视口下，`SheetContent` 宽度通过 `w-full sm:max-w-xl` 保证小屏占满、大屏保持 576px/672px 宽度，避免宽屏过度拉伸。
- **测试框架 (Vitest Browser + Playwright Chromium)**:
  - Vitest 浏览器测试通过真实 DOM 树断言，`Sheet` 渲染在 `DialogPortal` 中，依然暴露 `role="dialog"`，Heading 依然通过 `h2` 呈现，原测试用例中按 role 检索的行为大部分可平滑保留，仅需注意确认关闭与触发行为的微调。

## 风险与审查重点（Review Focus）

1. **真实无头浏览器测试的角色与选择器断言**：
   - 原测试用例使用 `getByRole('heading', { name: /新增摄像机资产/i })` 等。迁移为 Sheet 后，确认 `SheetTitle` 默认渲染的 HTML 标签及 ARIA 属性一致，避免测试因选择器失效报错。
2. **移动端与小屏幕自适应**：
   - 确保当视口宽度小于 640px（`sm:` 阈值）时，抽屉能够自适应占满宽度（`w-full`），且内部横向无滚动条溢出。
3. **正在提交/探测时的关闭防护**：
   - 当 `isPending` 为 true（正在进行 RTSP 连通性测试或网络提交）时，必须禁用背景遮罩点击关闭与 ESC 键盘关闭，防止异步操作处于中间不一致状态。
4. **外部表单与 Footer 提交联动**：
   - Footer 的提交按钮位于表单 DOM 树外，依靠 HTML5 `form="camera-action-form"` 属性关联。确保滚动容器重构后，表单 ID 关联无遗漏，回车键触发提交行为依然完好。
