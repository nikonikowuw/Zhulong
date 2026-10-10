# 前端审计日志视图与溯源管理 (Frontend Audit Logs)

## Goal

在 Web 前端控制台构建基于 `shadcn-admin` 与 TanStack 体系的系统操作与安全审计日志视图（`Audit Logs`），为单用户边缘视频智能分析设备（烛龙 Zhulong）提供高效、直观的历史操作追溯、安全异常定位与日志生命周期管理界面。

## Scope

1. **API 集成与类型系统 (`web/src/features/audit/api/` & `data/schema.ts`)**
   - 对接后端 `/api/v1/audit/logs` 的 `GET`（分页过滤查询）与 `DELETE`（条件清理/清空）接口。
   - 列表页大小默认为 20、最大为 100；过滤结果总数必须由服务端在分页前计算。
   - 定义完整的 `AuditLogDTO`、查询参数 `AuditLogsQuery` 及相关响应类型。
   - 使用 `@tanstack/react-query` 封装 `useAuditLogs` 与 `useClearAuditLogs` mutation，实现严格的服务端状态缓存与按需失效。

2. **视图与核心交互组件 (`web/src/features/audit/components/`)**
   - **数据表格 (`audit-table.tsx` & `audit-columns.tsx`)**：
     - 展示时间戳（本地化时区格式）、操作用户、操作动作（`action`）、操作目标（`target`）、客户端 IP、执行状态（`status` Badge）与操作动作按钮。
     - 状态 Badge 严格遵循 Dot-Indicator Outline 规范（成功为 Teal/Emerald，失败为 Destructive）。
   - **多维过滤工具栏 (`audit-toolbar.tsx`)**：
     - 集成 URL 状态同步（通过 `useTableUrlState`）。
     - 动作选项支持模糊搜索与多选；多选值通过重复的 `actions` 查询参数按 OR 语义进行服务端过滤，既有单值 `action` 参数继续兼容。
     - 支持 `status`（全部/成功/失败）过滤。
     - 支持 RFC3339 兼容的时间范围选择或快捷过滤。
   - **详情抽屉/对话框 (`audit-detail-dialog.tsx`)**：
     - 点击日志行或「查看详情」操作，以 Sheet 或 Dialog 形式展示完整元数据。
     - 美化渲染 `detail` JSON 数据（变更前后对比或调用上下文）与 `errorMsg`。
   - **日志清空对话框 (`audit-clear-dialog.tsx`)**：
     - 提供危险操作二次确认（支持清理全部日志或清理指定时间之前的日志）。

3. **路由与全局侧边栏导航集成**
   - 新增 TanStack Router 路由 `web/src/routes/_authenticated/audit/index.tsx`。
   - 在侧边栏导航配置 `sidebarData` 中添加「审计日志」入口（默认放置在 `General` 分组，配备 `ShieldAlert` 或 `ScrollText` 图标）。

4. **三语国际化支持 (`web/src/features/audit/locales/`)**
   - 提取所有 UI 文本至 `zh-Hans.json`、`zh-Hant.json`、`en.json`，并在 `web/src/lib/i18n.ts` 注册命名空间 `audit`。

## Acceptance Criteria

- [x] **数据拉取与展示**：进入 `/audit` 页面可正确分页加载后端审计日志，并在表格中清晰展示时间、用户、操作、目标、IP 和状态。
- [x] **多维过滤与 URL 同步**：筛选动作类型、状态或切换分页时，URL Search 参数实时同步，刷新页面能还原筛选状态。
- [x] **详情可读性**：点击单条记录可打开详情抽屉/弹窗，`detail` 字段若为合法 JSON 能自动格式化高亮展示，错误信息 `errorMsg` 明确醒目。
- [x] **日志清理与安全确认**：管理员可触发清理日志对话框，确认后成功调用 `DELETE /api/v1/audit/logs` 并自动刷新列表，伴随 Toast 提示。
- [x] **视觉风格一致性**：100% 遵循 `satnaing/shadcn-admin` 规范，深浅模式切换无色彩割裂，状态 Badge 符合 Dot-Indicator Outline 规范。
- [x] **国际化完整性**：简体中文、繁体中文、英文无遗漏硬编码，时区根据浏览器本地自动转换。
- [x] **代码质量门禁**：通过 `pnpm lint`、`pnpm build` (tsc) 与相关单元/组件测试，无类型错误，无 `any`。

## Non-Goals

- 本期不涉及前端客户端层导出数十万条海量日志的离线分析（设备端最大阈值 5,000 条 FIFO，专注于实时排障与合规溯源）。
- 保持既有单值 `action` 查询参数与响应结构兼容；action 多选使用可重复的 `actions` 查询参数，数据库结构不变。若请求同时提供非空 `actions` 和旧版 `action`，以后者 `actions` 为准。
