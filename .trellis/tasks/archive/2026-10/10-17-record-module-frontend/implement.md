# 执行计划：前端感知记录模块 (records feature)

## 执行步骤与检查清单

### Phase 1: 数据模型与 API 基础层 (Foundation)
- [x] 1.1 在 `web/src/features/records/data/schema.ts` 建立 Zod Schema 与类型定义：
  - `recognitionSchema`, `alarmSchema`, `captureSchema`
  - `recognitionListResponseSchema`, `alarmListResponseSchema`, `captureListResponseSchema`
  - `recordsSearchSchema` (支持 tab, view, page, pageSize, cameraId, startTime, endTime, preset, matched, personId, alarmType, targetType)
- [x] 1.2 编写 `web/src/features/records/data/schema.test.ts`，验证 Schema 校验与 fallback 默认值。
- [x] 1.3 在 `web/src/features/records/api/records-api.ts` 封装 API 请求客户端（使用 `src/lib/api-client.ts`），提供 List 与 Get Detail 方法。
- [x] 1.4 编写 `web/src/features/records/api/records-api.test.ts`，验证查询参数构建与返回值处理。
- 验证指令：`cd web && pnpm test -- src/features/records/data/schema.test.ts src/features/records/api/records-api.test.ts`

### Phase 2: 国际化与通用渲染基建 (i18n & Common Components)
- [x] 2.1 创建三语包：
  - `web/src/features/records/locales/zh-Hans.json`
  - `web/src/features/records/locales/zh-Hant.json`
  - `web/src/features/records/locales/en.json`
- [x] 2.2 在 `web/src/lib/i18n.ts` 中注册 `records` 命名空间。
- [x] 2.3 在 `web/src/features/records/components/common/` 封装通用组件：
  - `media-image.tsx`：统一解析 `/api/v1/media/content/{path}`，具备懒加载与加载失败 Fallback。
  - `bounding-box-canvas.tsx`：在原图上自适应渲染目标 Bounding Box（支持人脸框与人体框）。
- 验证指令：`cd web && pnpm build` 确认 i18n 资源和组件类型正确。

### Phase 3: Hooks 与三大业务视图 (Hooks & Views)
- [x] 3.1 编写 TanStack Query Hooks：
  - `web/src/features/records/hooks/use-recognitions.ts`
  - `web/src/features/records/hooks/use-alarms.ts`
  - `web/src/features/records/hooks/use-captures.ts`
- [x] 3.2 编写顶部筛选工具栏 `records-toolbar.tsx`：
  - 集成摄像头下拉（拉取摄像头列表）、时间范围选择器、视图切换切换器（Grid / Table）、搜索/刷新。
- [x] 3.3 编写业务视图组件：
  - 识别视图：`recognitions-view.tsx`（包含 Table 模式与 Card/Grid 模式）
  - 告警视图：`alarms-view.tsx`（包含 Table 模式与 Card/Grid 模式）
  - 抓拍视图：`captures-view.tsx`（包含 Table 模式与 Card/Grid 模式）
- 验证指令：`cd web && pnpm lint`

### Phase 4: 详情研判弹窗 (Detail Dialog)
- [x] 4.1 编写 `web/src/features/records/components/detail/record-detail-dialog.tsx`：
  - 支持传入选中的 Record（识别/告警/抓拍）。
  - 全图模式：展示全景图 + `BoundingBoxCanvas`。
  - 识别对比区：展示现场人脸小图与底库照片对比、置信度、候选人列表。
  - 属性解析区：展示 JSON 树形属性。
- [x] 4.2 编写 `record-detail-dialog.test.tsx` 验证详情弹窗交互。
- 验证指令：`cd web && pnpm test -- src/features/records/components/detail/record-detail-dialog.test.tsx`

### Phase 5: 主入口、路由与侧边栏接入 (Integration)
- [x] 5.1 组合 `web/src/features/records/index.tsx` 主入口，结合 URL Search Params 管理 Tab 与筛选。
- [x] 5.2 创建 TanStack Router 路由 `web/src/routes/_authenticated/records/index.tsx`，配置 `validateSearch`。
- [x] 5.3 在 `web/src/components/layout/data/sidebar-data.ts` 中将 `Records` 添加至侧边栏 General 分组。
- [x] 5.4 重新生成路由树 `pnpm build` 或等待 Vite 插件生成 `routeTree.gen.ts`。
- 验证指令：`cd web && pnpm test`

### Phase 6: 全链路质量门禁 (Quality Gate)
- [x] 6.1 执行前端代码检查与类型构建：`cd web && pnpm lint && pnpm format:check && pnpm test && pnpm build`。
- [x] 6.2 根目录执行完整门禁：`make check`。

### Phase 7: 动态类型筛选与交互优化 (Dynamic Types & UX Polish)
- [x] 7.1 后端暴露 `GET /records/captures/target-types` 和 `GET /records/alarms/alarm-types` 接口，提供数据库实际去重类型。
- [x] 7.2 前端新增 `useCaptureTargetTypes` 与 `useAlarmTypes` hooks，工具栏动态渲染下拉选择器代替生硬的文本输入。
- [x] 7.3 统一表格操作体验，移除冗余的详情操作列，支持整行点击研判。
- [x] 7.4 对齐各页面 Header 布局并默认采用 Table 视图，消除路由切换时的视觉跳动。
