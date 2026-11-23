# Proposal: 2026-11-23-channel-task-fullscreen-workbench

## 背景与目标

当前系统的通道任务配置（`ChannelTaskDialog`）集成了实时视频流播放、Canvas 周界/绊线 ROI 绘制、算法参数动态表单、24 小时布防时间表以及算力预算计量条，本质上是一个**多面板视听感知配置工作台**。

目前它仍寄生在 Radix `Dialog` 中，通过超大尺寸覆盖样式（`h-[94vh] w-[98vw] sm:max-w-[97vw] 2xl:max-w-[1680px]`）勉强撑开。这种“伪装成居中弹窗的工作台”存在明显体验问题：
1. **视野与画布空间浪费**：保留居中弹窗的圆角边框与 94vh/98vw 外边距，周围露出一圈尴尬的半透明黑框，挤压了视频画布与右侧算法配置面板的可用空间；
2. **双重滚动条隐患**：受 `DialogContent` 固定居中定位与视口高度限制，在 1080p 笔记本或小屏设备上容易出现外层弹窗与内层面板的滚动冲突；
3. **语义与交互割裂**：重度多面板工作台按模态对话框管理，缺乏全屏工作台的沉浸感与明确退出/返回语义。

**本次变更目标**：
将 `ChannelTaskDialog` 及其头部/底部子组件从居中 `Dialog` 容器彻底重构为**全屏沉浸式工作台（Fullscreen Workbench）**，占满 `100vw × 100dvh` 视口空间、消除外围遮罩边框与双重滚动冲突，同时保持现有 `TasksDialogs` 上下文状态、ROI 绘制快捷键、算力预算校验与所有单元测试完整通过。

## In Scope（本次包含）

- **全屏工作台容器重构**：
  - 将 `channel-task-dialog.tsx` 的外层容器由 Radix `<Dialog>` / `<DialogContent>` 迁移为固定全屏工作台容器（`fixed inset-0 z-50 flex h-dvh w-screen flex-col overflow-hidden bg-background`），通过 Portal 挂载并保持无障碍语义（`role="dialog"`、`aria-modal="true"`、`aria-labelledby`）与键盘 `Escape` 关闭支持（不与 Canvas 快捷键冲突）。
- **子组件与 Radix Dialog 解耦**：
  - `channel-task-dialog-header.tsx`：将 `DialogHeader` / `DialogTitle` / `DialogDescription` 替换为语义化 `<header>` / `<h2>` / `<p>`，提供更清晰的顶部工作台栏（含关闭/返回操作）。
  - `channel-task-dialog-footer.tsx`：将 `DialogFooter` 替换为语义化 `<footer>`，保持底部操作栏常驻固定。
- **向后兼容与测试覆盖**：
  - 保留 `ChannelTaskDialog` 导出名称与 `ChannelTaskDialogProps` 契约，并补充语义化别名 `ChannelTaskWorkbench`（如适用），确保 `tasks-dialogs.tsx` 无缝衔接。
  - 更新并增强 `channel-task-dialog.test.tsx`，补充全屏视口尺寸占满（无外围居中边距）、无横向/外层溢出、Escape 退出与原有 ROI/算法配置回归断言。

## Out of Scope（明确不做）

- **不新增独立二级路由**：不修改 `web/src/routes/**` 与 `routeTree.gen.ts`，继续由 `useTasksContext()` 驱动，保留任务列表的筛选、展开行和分页位置。
- **不改动业务逻辑与数据模型**：不修改 `useChannelTaskDrafts`、`useSyncChannelAlgorithms`、算力预算算法、`RegionCanvas` / `TripwireCanvas` 绘制逻辑及任何后端接口。
- **不改动其他模块的 Dialog/Sheet**：不影响 `persons`、`gb28181`、`api-tokens`、`spatial` 等其他特性的弹窗。

## 影响范围（模块 / 接口契约 / Specs & Rules）

- **涉及文件**：
  - `web/src/features/tasks/components/channel-task-dialog.tsx`
  - `web/src/features/tasks/components/channel-task-dialog-header.tsx`
  - `web/src/features/tasks/components/channel-task-dialog-footer.tsx`
  - `web/src/features/tasks/components/channel-task-dialog.test.tsx`
  - `web/src/features/tasks/components/tasks-dialogs.tsx`（校验接口兼容性）
- **挂载规格与规则**：
  - Specs: `frontend/index.md`, `frontend/development-guidelines.md`, `frontend/quality-guidelines.md`
  - Rules: `web/frontend-api-boundary`, `web/frontend-browser-tests`, `web/frontend-route-boundary`
