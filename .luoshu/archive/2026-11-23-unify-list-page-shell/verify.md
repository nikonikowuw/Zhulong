# 验收契约：统一列表页骨架

- `base_commit`：`0bc6d7e142b50ba3f1a7a677554fb1b1ab6f2211`
- 验证对象：该 commit 到工作区 HEAD 的完整 diff。
- 验证方式：只读评审 + 下列命令；不修改源码、不修改本文件。

## 1. 可观察的通过判据

| ID | 判据 | 验证方法 |
| :--- | :--- | :--- |
| V1 | `ListPage` 渲染 `<Header fixed>` 与 `<Main className='flex flex-1 flex-col gap-4 sm:gap-6'>`；标题行节点带 `[data-slot='list-page-header']` 且类名含 `flex flex-wrap items-end justify-between gap-2`；标题块带 `min-w-0 flex-1`；描述带 `max-w-3xl`；未传 `actions` 时不存在 `[data-slot='list-page-actions']` | `web/src/components/list-page.test.tsx` |
| V2 | `DataTableShell` 容器节点带 `[data-slot='data-table-container']`，类名**精确**为 `rounded-md border border-border bg-card`（`cn` 合并后无多余边框/圆角/背景类）；`totalRows === 0` 时渲染结果中不存在分页条（无 `nav[aria-label]`/页码按钮）；`totalRows > 0` 时存在 | `web/src/components/data-table/shell.test.tsx` |
| V3 | loading 分支渲染行数等于 `skeletonRows`（默认 5）；`isError` 时只出现 `errorState`，不出现 `emptyState`；`rows.length > 0` 时 `renderRow` 的返回值被采用（返回两行 Fragment 时 `tbody` 有 4 个 `tr`）；`meta.className` 与 `meta.tdClassName` 同时落到 `td` | `web/src/components/data-table/shell.test.tsx` |
| V4 | 同一张表内可排序与不可排序列头的包裹层高度均为 32px；不可排序列头包裹层含 `px-3`；可排序列头仍渲染 ghost `Button`（`h-8`） | `web/src/components/data-table/column-header.test.tsx` |
| V5 | 5 个列表页的真实浏览器测试中都能命中 `[data-slot='data-table-container']`，且页面标题文本正确 | 5 个 `*-table.test.tsx` 与 `persons/index.test.tsx` |
| V6 | 5 个页面入口不再 import `@/components/layout/header` / `@/components/layout/main`；ESLint 对 5 个文件零 error | `cd web && pnpm exec eslint src/features/persons/index.tsx src/features/cameras/index.tsx src/features/algorithms/index.tsx src/features/audit/index.tsx src/features/tasks/index.tsx` |
| V7 | 既有交互零回归：persons 的表格/网格切换、批量选择、筛选；cameras 的状态筛选与分页；algorithms 的排序；audit 的分页；tasks 的展开/收起 | 对应 `*-table.test.tsx` 全绿，且断言数量不少于 `base_commit` |
| V8 | `make frontend-check` 全绿（format / lint / tsc / test） | `make frontend-check` |

## 2. 否定边界（必须不成立）

- N1：不得改动 `web/src/components/ui/**` 任何文件。
- N2：不得新增/修改业务 API、`manualPagination` / `manualSorting` / `manualFiltering` 的取值，或 `web/src/features/*/api/**`；persons 仍为服务端分页。
- N3：不得改动 `gb28181`、`records`、`image-search`、`live`、`spatial` 的页面与表格；`gb28181-devices-table.tsx` 的 `rounded-xl …` 容器保持不变。
- N4：不得向 `web/src/locales/**/common.json` 新增业务文案 key；空态/错误态文案仍由各特性 locale 提供。
- N5：不得削弱测试：无 `.skip` / `.only` / `expect.soft`，无删除既有断言；5 个表测试的断言数为净增。
- N6：不得改动 Go 侧、`internal/webui/dist`、`web/src/routes/**`、`web/src/routeTree.gen.ts`。

## 3. 必跑命令

```bash
cd web && pnpm test -- src/components/list-page.test.tsx
cd web && pnpm test -- src/components/data-table/shell.test.tsx
cd web && pnpm test -- src/components/data-table/column-header.test.tsx
cd web && pnpm test -- src/features/persons/components/persons-table.test.tsx
cd web && pnpm test -- src/features/persons/index.test.tsx
cd web && pnpm test -- src/features/cameras/components/cameras-table.test.tsx
cd web && pnpm test -- src/features/algorithms/components/algorithms-table.test.tsx
cd web && pnpm test -- src/features/audit/components/AuditTable.test.tsx
cd web && pnpm test -- src/features/tasks/components/tasks-table.test.tsx
cd web && pnpm exec eslint src/features/persons/index.tsx src/features/cameras/index.tsx src/features/algorithms/index.tsx src/features/audit/index.tsx src/features/tasks/index.tsx
cd web && pnpm exec prettier --check src/components/list-page.tsx src/components/list-page.test.tsx src/components/data-table/shell.tsx src/components/data-table/shell.test.tsx src/components/data-table/column-header.tsx src/components/data-table/column-header.test.tsx eslint.config.js
make frontend-check
```

## 4. 静态复核清单

- [ ] `git diff --stat 0bc6d7e...HEAD` 只包含 proposal「影响范围」列出的文件。
- [ ] `.luoshu/specs/frontend/theme-and-i18n.md` §4.1 已不再出现「照抄 persons / cameras」这类以页面为参照的表述，且每个条款都能对应到 `ListPage` / `DataTableShell` 的具名结构。
- [ ] 规格新增的判定条件表覆盖：服务端分页 → 禁客户端排序；客户端分页前提；视图切换仅在有 grid 分支时；自定义工具栏走 `toolbarActions`。
- [ ] 例外白名单包含 `gb28181`、`records`、`image-search` / `live` / `spatial`，并写明理由。
- [ ] `web/eslint.config.js` 的限制只作用于 5 个入口文件，未影响其他页面。
- [ ] `DataTableShell` 未引入 `useTranslation`，未新增 i18n 依赖。
