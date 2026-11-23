# 统一列表页骨架：ListPage + DataTableShell

## 背景与目标

`web/src/components/data-table/` 目前只导出 `DataTableToolbar` / `DataTablePagination` / `DataTableColumnHeader` / `DataTableBulkActions`，**没有表格骨架**。于是每个列表页各自手写 `<Header fixed>`、`<Main>`、标题行、`rounded-md border border-border bg-card` 容器、工具栏包装和分页可见性判断，视觉一致性只能靠 `.luoshu/specs/frontend/theme-and-i18n.md` §4.1 的一句「照抄 persons / cameras」和评审人肉眼维护。结果是：

- `cameras` / `audit` / `algorithms` 的标题块缺 `min-w-0 flex-1` 与描述 `max-w-3xl`，长文案会把主操作按钮挤到第二行最左；
- `gb28181` 的表格容器已漂移为 `rounded-xl border-border/80 bg-card/75 shadow-2xs`；
- 列头在有排序与无排序之间差一个 `h-8 px-3` 的 ghost Button，表格首列文本基线在不同页面间横移约 12px；
- 分页条在 0 行时是否渲染各页自行决定；
- 没有任何测试断言列表页骨架，也没有机制阻止新页面继续复制粘贴。

目标：把 §4.1 的每条规则落成**组件结构与具名插槽**，用 `ListPage` + `DataTableShell` 收敛页面骨架，用测试与 lint 建立不可绕过的门禁，并同步改写规格。

## In Scope

1. 新增 `web/src/components/list-page.tsx`：`Header`（顶栏四件套）+ `Main` + 标题行（`data-slot='list-page-header'` / `list-page-actions'`）。
2. 新增 `web/src/components/data-table/shell.tsx`：工具栏行、`data-slot='data-table-container'`、表头/单元格 meta 类名、loading / error / rows / empty 四态分支、行级 `renderRow` 逃生口、分页可见性。
3. 改造 `web/src/components/data-table/column-header.tsx`：不可排序列也渲染与 ghost Button 同高同内边距的容器。
4. 迁移 5 个 CRUD 列表：`persons`、`cameras`、`algorithms`、`audit`、`tasks`（任务页用 `renderRow` 保留展开行）。
5. 门禁：两个新组件测试、列头等高测试、5 个既有表测试补 `data-slot` 断言、ESLint 禁止 5 个页面入口再手写 `Header` / `Main`。
6. 改写 `.luoshu/specs/frontend/theme-and-i18n.md` §4.1：条款绑定组件/槽位，新增「分页模型与排序能力」判定条件表与例外白名单。

## Out of Scope

- `gb28181` 设置页内嵌表（`settings/gb28181` 的 cockpit 表面，不是列表页骨架）——写入例外白名单，本次不改其容器类名。
- `records` 的 `RecordsListShell`（媒体工作台，grid + table 双视图）、`image-search` / `live` / `spatial` 工作台。
- `persons` 主操作按钮层级收敛（6 → 3 + 「更多」）：产品决策，且已被 `persons-primary-buttons.test.tsx` 覆盖。
- 工具栏筛选器的「按需添加」形态（`pin` 常驻 + `+ 筛选` 折叠 + 已生效 chip）：属于交互行为变更，另开后续变更 `toolbar-filter-builder`，排在本次 apply 之后，避免同一批 `*-table.tsx` 两处并发改动。
- 分页模型变更：`persons` 保持服务端分页（`manualPagination`，10 条/页）；其余四页保持客户端分页。本变更只统一**外观与结构**，不动数据获取与排序语义。
- 主题 token、配色、间距尺度、`components/ui/**` 的任何改动。

## 影响范围

| 层 | 文件 |
| :--- | :--- |
| 共享组件 | `web/src/components/list-page.tsx`（新增）、`web/src/components/data-table/shell.tsx`（新增）、`web/src/components/data-table/column-header.tsx`（改） |
| 页面 | `web/src/features/{persons,cameras,algorithms,audit,tasks}/index.tsx` 及其 `*-table.tsx`（`tasks` 为 `tasks-table.tsx` + `tasks-table-detail.tsx`） |
| 契约 | `.luoshu/specs/frontend/theme-and-i18n.md` §4.1 |
| 门禁 | `web/eslint.config.js`、`web/src/components/list-page.test.tsx`、`web/src/components/data-table/shell.test.tsx`、`web/src/components/data-table/column-header.test.tsx`、5 个既有 `*-table.test.tsx` |

## 验收摘要

- V1：`ListPage` 渲染的标题行包含 spec 规定的类名与两个 `data-slot`，主操作位于 `list-page-actions`。
- V2：`DataTableShell` 容器类名精确等于 `rounded-md border border-border bg-card`，`totalRows === 0` 时不渲染分页条。
- V3：`DataTableShell` 的 loading / error / empty / rows 四态互斥，`renderRow` 可返回多行 Fragment。
- V4：同一张表内可排序与不可排序列头等高（32px），不可排序列头带 `px-3`。
- V5：5 个列表页在真实浏览器测试中都能命中 `[data-slot='data-table-container']`。
- V6：5 个页面入口不再 import `@/components/layout/header` / `@/components/layout/main`，ESLint 通过。
- V7：既有交互测试（筛选、排序、分页、批量操作、任务展开）零回归。
- V8：`make frontend-check` 全绿。
