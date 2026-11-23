# 任务：统一列表页骨架

> 依赖拓扑：共享组件（1.x）→ 页面迁移（2.x）→ lint 门禁（3.1）→ 规格改写（3.2）→ 全量门禁（3.3）。
> 每个任务的 `check` 均为可独立执行的增量验证，不依赖后续任务。

- [x] 1.1 (deps: none) 新增 `web/src/components/list-page.tsx`，实现 `ListPage`（`Header fixed` + 顶栏四件套 + `Main` + 标题行 `data-slot='list-page-header'` / `list-page-heading` / `list-page-actions`），并新增 `web/src/components/list-page.test.tsx` 覆盖标题/描述/`actions` 缺省三态与 `min-w-0 flex-1`、`max-w-3xl` 类名（check: `cd web && pnpm test -- src/components/list-page.test.tsx`）

- [x] 1.2 (deps: none) 新增 `web/src/components/data-table/shell.tsx`，实现 `DataTableShell` 的工具栏行、`data-slot='data-table-container'` 容器、表头/单元格 `meta.className` + `thClassName` / `tdClassName`、`isLoading → isError → rows → empty` 四态分支、`renderRow` 逃生口、`totalRows === 0` 不渲染分页条；新增 `web/src/components/data-table/shell.test.tsx` 覆盖容器类名、0 行无分页、骨架行数、错误/空态、`renderRow` 多行、`tdClassName`（check: `cd web && pnpm test -- src/components/data-table/shell.test.tsx`）

- [x] 1.3 (deps: none) 修改 `web/src/components/data-table/column-header.tsx`，不可排序分支改为 `flex h-8 items-center px-3` 容器；新增 `web/src/components/data-table/column-header.test.tsx`，用一列可排序 + 一列不可排序断言两个 `th` 包裹层高度均为 32px（check: `cd web && pnpm test -- src/components/data-table/column-header.test.tsx`）

- [x] 2.1 (deps: 1.1, 1.2, 1.3) 迁移 persons：`web/src/features/persons/index.tsx` 改 `<ListPage title description actions={<PersonsPrimaryButtons/>}>` 并删除 `Header`/`Main`/顶栏 import；`web/src/features/persons/components/persons-table.tsx` 改用 `DataTableShell`（`toolbar` / `toolbarActions={<ViewModeSwitch/>}` / `skeletonRows={pageSize}` / `loadingRow` / `emptyState` / `errorState`，`totalRows={data?.total ?? 0}`），保留 `aria-busy` 包装与 grid 分支；在 `persons-table.test.tsx` 补 `data-slot` 断言（check: `cd web && pnpm test -- src/features/persons/components/persons-table.test.tsx src/features/persons/index.test.tsx`）

- [x] 2.2 (deps: 1.1, 1.2, 1.3) 迁移 cameras：`web/src/features/cameras/index.tsx` 改 `ListPage`；`web/src/features/cameras/components/cameras-table.tsx` 改用 `DataTableShell`（`totalRows={table.getFilteredRowModel().rows.length}`，骨架/空态/错误行改为插槽），补 `data-slot` 断言（check: `cd web && pnpm test -- src/features/cameras/components/cameras-table.test.tsx`）

- [x] 2.3 (deps: 1.1, 1.2, 1.3) 迁移 algorithms：`web/src/features/algorithms/index.tsx` 与 `web/src/features/algorithms/components/algorithms-table.tsx` 同上，补 `data-slot` 断言（check: `cd web && pnpm test -- src/features/algorithms/components/algorithms-table.test.tsx`）

- [x] 2.4 (deps: 1.1, 1.2, 1.3) 迁移 audit：`web/src/features/audit/index.tsx` 与 `web/src/features/audit/components/AuditTable.tsx` 改用 `ListPage` / `DataTableShell`（`AuditToolbar` 作为 `toolbar`，`totalRows={total}`），补 `data-slot` 断言（check: `cd web && pnpm test -- src/features/audit/components/AuditTable.test.tsx`）

- [x] 2.5 (deps: 1.1, 1.2, 1.3) 迁移 tasks：`web/src/features/tasks/index.tsx` 改 `ListPage`；`web/src/features/tasks/components/tasks-table-detail.tsx` 改用 `DataTableShell`，以 `renderRow` 保留「主行 + `row.getIsExpanded()` 展开行」的 Fragment 结构，`skeletonRows={5}`，补 `data-slot` 断言与展开行仍可切换的断言（check: `cd web && pnpm test -- src/features/tasks/components/tasks-table.test.tsx`）

- [x] 3.1 (deps: 2.1, 2.2, 2.3, 2.4, 2.5) 在 `web/eslint.config.js` 追加一段 `files` 仅含 5 个入口文件的配置，`no-restricted-imports` 禁止 `@/components/layout/header` 与 `@/components/layout/main`，message 指向 `<ListPage>`（check: `cd web && pnpm exec eslint src/features/persons/index.tsx src/features/cameras/index.tsx src/features/algorithms/index.tsx src/features/audit/index.tsx src/features/tasks/index.tsx`）

- [x] 3.2 (deps: 3.1) 改写 `.luoshu/specs/frontend/theme-and-i18n.md` §4.1：条款绑定 `ListPage` / `DataTableShell` 与 `data-slot`，新增分页模型/排序/视图切换/工具栏判定条件表与例外白名单（gb28181、records、image-search/live/spatial），保留空态铁律（check: `grep -q "DataTableShell" .luoshu/specs/frontend/theme-and-i18n.md`）

- [x] 3.3 (deps: 3.2) 全量门禁：对改动文件跑 prettier，然后跑一次 `make frontend-check`（check: `make frontend-check`）
