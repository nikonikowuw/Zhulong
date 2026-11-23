# Tasks: 2026-11-23-camera-alert-form-drawer-sheet

> 必须按自底向上依赖拓扑顺序排列，严禁前向依赖。每个任务标注前置依赖并注明增量验证命令。

## 1. 实施任务

- [x] 1.1 (deps: none) 摄像头新增/编辑容器抽屉化：将 `CamerasActionDialog` 外层容器由 `Dialog` 迁移为 `Sheet`，优化 100vh 垂直滚动区域与底部停靠操作栏，并同步更新单测断言 — `web/src/features/cameras/components/cameras-action-dialog.tsx`, `web/src/features/cameras/components/cameras-action-dialog.test.tsx` (check: `cd web && pnpm test -- src/features/cameras/components/cameras-action-dialog.test.tsx`)
- [x] 1.2 (deps: 1.1) 告警推送通道配置抽屉化：将 `AlertDestinationDialog` 外层容器由 `Dialog` 迁移为 `Sheet`，重构多板块长表单滚动流与底部常驻提交栏，并同步更新单测断言 — `web/src/features/alert-push/components/alert-destination-dialog.tsx`, `web/src/features/alert-push/components/alert-destination-dialog.test.tsx` (check: `cd web && pnpm test -- src/features/alert-push/components/alert-destination-dialog.test.tsx`)
- [x] 1.3 (deps: 1.2) 页面集成与类型对齐：检查并验证 `cameras-dialogs.tsx` 与 `alert-push-page.tsx` 的调用点，确保属性契约兼容且 TypeScript 类型检查无报错 — `web/src/features/cameras/components/cameras-dialogs.tsx`, `web/src/features/alert-push/components/alert-push-page.tsx` (check: `cd web && pnpm type-check`)
- [x] 1.4 (deps: 1.3) 特性回归与真实浏览器测试门禁：运行摄像头与告警推送的全量前端单元测试，确保真实 Chromium 下行为断言、探测中禁用态与提交响应完全通过 — `web/src/features/cameras/`, `web/src/features/alert-push/` (check: `cd web && pnpm test -- src/features/cameras/ src/features/alert-push/`)
