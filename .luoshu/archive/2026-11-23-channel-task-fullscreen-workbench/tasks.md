# Tasks: 2026-11-23-channel-task-fullscreen-workbench

> 必须按自底向上依赖拓扑顺序排列，严禁前向依赖。每个任务标注前置依赖并注明增量验证命令。

## 1. 实施任务

- [x] 1.1 (deps: none) 工作台头部与底部子组件解耦：移除 `DialogHeader` / `DialogTitle` / `DialogDescription` / `DialogFooter` 对 Radix Dialog 上下文的依赖，改为标准语义化 `<header>` / `<h2>` / `<p>` / `<footer>` 元素并暴露标题/描述 ARIA ID — `web/src/features/tasks/components/channel-task-dialog-header.tsx`, `web/src/features/tasks/components/channel-task-dialog-footer.tsx` (check: `cd web && pnpm type-check`)
- [x] 1.2 (deps: 1.1) 全屏工作台主容器重构：将 `ChannelTaskDialog` 由 Radix `Dialog` 迁移为 `createPortal` 挂载的 `fixed inset-0 z-50 h-dvh w-screen` 全屏工作台，接入 `role="dialog"`/`aria-modal`、`document.body` 滚动锁定与 `Escape` 键安全关闭防护 — `web/src/features/tasks/components/channel-task-dialog.tsx` (check: `cd web && pnpm type-check`)
- [x] 1.3 (deps: 1.2) 真实浏览器测试更新与全屏断言增强：更新 `channel-task-dialog.test.tsx`，保留既有 ROI/算法配置/快捷键回归测试，并新增全屏视口覆盖（无外围居中留白）、body 滚动锁定与恢复、`Escape` 关闭及保存中保护断言 — `web/src/features/tasks/components/channel-task-dialog.test.tsx` (check: `cd web && pnpm test -- src/features/tasks/components/channel-task-dialog.test.tsx`)
- [x] 1.4 (deps: 1.3) 任务模块全量回归与代码检查：确认 `tasks-dialogs.tsx` 集成正常，运行 `tasks` 特性全部浏览器测试与前端 lint/type-check — `web/src/features/tasks/` (check: `cd web && pnpm test -- src/features/tasks/ && pnpm type-check`)
