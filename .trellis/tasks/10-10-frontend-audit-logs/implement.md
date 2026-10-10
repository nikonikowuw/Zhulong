# 前端审计日志视图与溯源管理实施计划 (Implementation Plan)

## 实施步骤清单

### 阶段 1：数据模型、API 客户端与国际化底座
- [x] 1.1 **数据模型与类型声明**：在 `web/src/features/audit/data/schema.ts` 中定义 `AuditLog` 模式、查询参数 `AuditLogsQuery` 及相关 Zod Schema 与 TypeScript 类型。
  - 验证：类型定义编译无报错，字段与 Go `AuditLogDTO` 严格一致。
- [x] 1.2 **API 请求封装**：在 `web/src/features/audit/api/audit-api.ts` 中基于内置 `request` 客户端实现 `getAuditLogs` 与 `clearAuditLogs` 函数。
  - 验证：正确处理分页、过滤参数序列化及统一响应信封解包。
- [x] 1.3 **TanStack Query Hooks**：在 `web/src/features/audit/hooks/use-audit-logs.ts` 中实现 `useAuditLogs` 与 `useClearAuditLogs` mutation。
  - 验证：缓存 Key 合理，mutation 成功后自动触发 queryKey 失效。
- [x] 1.4 **三语国际化配置**：
  - 新增 `web/src/features/audit/locales/en.json`、`zh-Hans.json`、`zh-Hant.json`。
  - 在 `web/src/lib/i18n.ts` 中注册 `audit` 命名空间。
  - 验证：控制台无缺少翻译 key 警告。

### 阶段 2：核心组件与表格交互构建
- [x] 2.1 **上下文与状态容器**：创建 `audit-provider.tsx`，管理当前选中的单条审计记录（用于详情查看）与清空确认弹窗的打开状态。
- [x] 2.2 **列定义与状态徽章**：在 `audit-columns.tsx` 中编写 TanStack Table 列配置。
  - 严格采用 Dot-Indicator Outline 规范渲染 `status`（成功/失败）。
  - 时间列采用本地化格式化器。
  - 操作列提供「查看详情」按钮。
- [x] 2.3 **过滤工具栏**：在 `AuditToolbar.tsx` 中实现 action 选项模糊搜索/多选、执行状态下拉筛选、重置按钮，与 `useTableUrlState` 联动；不提供用户/目标/IP 全局搜索。
- [x] 2.4 **表格展示与状态反馈**：在 `audit-table.tsx` 中实现数据表格、骨架屏 Loading、空数据 Empty 引导态、错误重试态以及翻页控件集成。
- [x] 2.5 **详情查看抽屉/弹窗**：在 `audit-detail-dialog.tsx` 中实现结构化详情展示，支持 JSON 缩进排版渲染、一键复制，以及异常信息的醒目展现。
- [x] 2.6 **日志清理确认弹窗**：在 `audit-clear-dialog.tsx` 中实现危险二次确认，支持全量清空或清理早于指定时间的日志。
- [x] 2.7 **顶部主操作区与主页面集成**：编写 `audit-primary-buttons.tsx` 与 `web/src/features/audit/index.tsx`。

### 阶段 3：路由挂载、侧边栏导航与端到端贯通
- [x] 3.1 **TanStack Router 路由创建**：创建 `web/src/routes/_authenticated/audit/index.tsx`，配置 URL Search Schema 验证。
- [x] 3.2 **侧边栏导航注册**：在 `web/src/components/layout/data/sidebar-data.ts` 的 `General` 分组中增加「审计日志」菜单项。
- [x] 3.3 **路由树更新**：运行 Vite 构建或路由生成，确认 `routeTree.gen.ts` 包含 `/audit` 路由。

### 阶段 4：质量检查、规范审计与回归测试
- [x] 4.1 **静态代码检查与格式化**：
  - `pnpm lint` 通过。
  - 审计功能及本任务触及的前端文件通过定向 Prettier 检查。
  - 仓库级 `pnpm format:check` 已运行，但报告 11 个未修改的 `settings` 文件；这些文件不属于本任务，未作格式改动。
- [x] 4.2 **类型编译与打包构建**：
  - 运行 `pnpm build`（含 `tsc -b`）确保类型安全与构建成功。
- [x] 4.3 **单元/组件测试**：
  - 为审计日志关键渲染与状态徽标编写或扩充测试用例，运行 `pnpm test` 验证。

---

### 阶段 5：审查问题修复
- [x] 5.1 **服务端多 action 过滤**：增加可重复 `actions` 查询参数，保留单值 `action` 兼容语义；若两种参数同时出现，以非空 `actions` 为准；Store 在计数与分页前以 OR 语义过滤多个动作。
  - 验证：Handler 测试覆盖同时传入两种参数时 `actions` 优先，Store/Handler 测试覆盖多 action、与 status/time 的组合及分页 total；既有单 action 请求仍通过。
- [x] 5.2 **URL 驱动过滤**：让 `useTableUrlState` 在浏览器前进/后退后从 URL 派生筛选状态；移除未列入 PRD 的用户/目标/IP 全局搜索，并让审计表格只使用服务端过滤结果。
  - 验证：Hook 测试覆盖外部 Search 更新，审计过滤测试确认多选参数和 URL 状态一致。
- [x] 5.3 **输入和响应运行时校验**：用 Zod 校验分页/清理 API 响应、RFC3339 日期 Search 参数和 `pageSize`（1..100）。
  - 验证：畸形/缺失响应和非法日期的回归测试。
- [x] 5.4 **审查规范修复**：补齐审计 UI 及共享筛选/分页控件的辅助文案翻译；复用状态徽章；JSON 合法内容高亮；统一组件文件名、弹窗状态 Hook、稳定 Skeleton keys、RTL 逻辑类与日期范围类型。
  - 验证：组件测试覆盖未知状态、JSON token、高亮文案；`pnpm lint` 无命名/Hook/key/RTL 违规。
- [x] 5.5 **全量验证**：运行前后端测试、lint、格式检查与生产构建。
  - 已通过：前端测试（52 个文件、241 个测试）、lint、build；`go test -race ./internal/audit`；任务涉及的前端文件定向 Prettier 检查；`git diff --check`。
  - `make go-check` 已通过；新增 Store 回归测试之后再次运行 `go test -race ./internal/audit`。
  - 仓库级 `pnpm --dir web format:check` 已运行，唯一失败是 11 个未修改的 `settings` 文件；本任务文件全部通过定向格式检查。此为已记录的仓库基线例外。

---

## 验证命令与检查点

```bash
cd web
pnpm lint
pnpm format:check
pnpm build
pnpm test
```

## 回滚策略 (Rollback Points)
- 若路由或组件变更引入意外阻塞，可直接移除 `web/src/routes/_authenticated/audit/` 与 `web/src/features/audit/` 目录，并撤销 `sidebar-data.ts` 与 `i18n.ts` 的注册项，系统核心功能不受任何影响。
