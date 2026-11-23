# 设计：统一列表页骨架

## 1. 设计原则

1. **结构即契约**：§4.1 里每一条「必须有 / 必须不要」都落成组件的固定 DOM 结构与 `data-slot`，评审不再依赖肉眼比对两个页面。
2. **只有一处能改**：`rounded-md border border-border bg-card`、工具栏行、标题行、分页可见性只在两个共享组件里出现；业务页面通过 props/插槽传内容，不传布局。
3. **不吞业务差异**：分页模型（服务端 vs 客户端）、排序能力、视图切换、自定义工具栏、展开行都是真实差异，用显式 prop 表达并在规格里写成判定条件，而不是强行拉平。
4. **零新增 i18n key**：错误/空态/骨架屏仍是各特性的本地化 JSX，通过 `errorState` / `emptyState` / `loadingRow` 插槽传入，避免把 `common.json` 变成第二套业务文案。

## 2. `ListPage`（新增 `web/src/components/list-page.tsx`）

```ts
type ListPageProps = {
  title: string
  description?: React.ReactNode
  /** 标题行右侧主操作，通常是 <XxxPrimaryButtons /> */
  actions?: React.ReactNode
  children: React.ReactNode
}

export function ListPage(props: ListPageProps): React.JSX.Element
```

结构契约（顺序固定，不允许页面覆盖）：

```tsx
<>
  <Header fixed>
    <Search className='me-auto' />
    <LanguageSwitch />
    <ThemeSwitch />
    <ConfigDrawer />
    <ProfileDropdown />
  </Header>

  <Main className='flex flex-1 flex-col gap-4 sm:gap-6'>
    <div data-slot='list-page-header' className='flex flex-wrap items-end justify-between gap-2'>
      <div data-slot='list-page-heading' className='min-w-0 flex-1'>
        <h2 className='text-2xl font-bold tracking-tight'>{title}</h2>
        {description ? (
          <p className='max-w-3xl text-sm text-muted-foreground'>{description}</p>
        ) : null}
      </div>
      {actions ? <div data-slot='list-page-actions'>{actions}</div> : null}
    </div>
    {children}
  </Main>
</>
```

- 顶栏四件套与 `Header fixed` 的写法来自现有 9 个页面中完全一致的那一段，抽出来是纯删除重复，不引入新视觉。
- `Header` / `Main` 从 `@/components/layout/header`、`@/components/layout/main` 引入，**只在 `ListPage` 内部**；5 个列表页入口不再直接依赖它们（由 lint 强制）。
- 页面内容（Provider、Dialogs、`DataTableShell`）全部通过 `children` 进入，`ListPage` 不感知业务。

## 3. `DataTableShell`（新增 `web/src/components/data-table/shell.tsx`）

```ts
export type DataTableShellProps<TData> = {
  table: Table<TData>
  /** 工具栏主体，通常是 <DataTableToolbar /> */
  toolbar?: React.ReactNode
  /** 工具栏右侧附加控件，如 <ViewModeSwitch /> */
  toolbarActions?: React.ReactNode
  /** 过滤后、分页前的总行数：为 0 时不渲染分页条（空态铁律） */
  totalRows: number
  isLoading?: boolean
  isError?: boolean
  /** 骨架行数，默认 5；服务端分页页传 pageSize */
  skeletonRows?: number
  /** 单行骨架（调用方按自己的列宽渲染） */
  loadingRow?: React.ReactNode
  /** 必须是 <TableRow>；colSpan 由调用方按自己的列数给 */
  emptyState?: React.ReactNode
  errorState?: React.ReactNode
  /** 逃生口：任务页需要「主行 + 展开行」的 Fragment */
  renderRow?: (row: Row<TData>) => React.ReactNode
  /** 覆盖默认分页槽 */
  footer?: React.ReactNode
  className?: string
}
```

渲染结构：

```tsx
<div className='flex flex-1 flex-col gap-4'>
  {(toolbar || toolbarActions) ? (
    <div data-slot='data-table-toolbar' className='flex flex-wrap items-center justify-between gap-2'>
      <div className='flex-1'>{toolbar}</div>
      {toolbarActions}
    </div>
  ) : null}

  <div data-slot='data-table-container' className={cn('rounded-md border border-border bg-card', className)}>
    <Table className='border-separate border-spacing-0'>
      <TableHeader>{/* TableHead: cn(meta?.className, meta?.thClassName) */}</TableHeader>
      <TableBody>{/* 四态分支 */}</TableBody>
    </Table>
  </div>

  {footer ?? (totalRows > 0 ? <DataTablePagination table={table} /> : null)}
</div>
```

四态分支优先级固定为 `isLoading → isError → rows.length > 0 → emptyState`，互斥且不叠加：

- **loading**：`Array.from({ length: skeletonRows })` 重复 `loadingRow`（外层 `<Fragment key={index}>`）；缺省渲染通用骨架行，每列一个 `<Skeleton className='h-4 w-full' />`。
- **error**：直接渲染 `errorState`（调用方提供本地化的 `t('table.loadError')` + 重试按钮）。
- **rows**：`renderRow ? renderRow(row) : <DefaultRow row={row} />`。
- **empty**：渲染 `emptyState`，**不渲染**分页条。

默认行：

```tsx
<TableRow
  key={row.id}
  data-state={row.getIsSelected() ? 'selected' : undefined}
  className='group'
>
  {row.getVisibleCells().map((cell) => (
    <TableCell key={cell.id} className={cn(cell.column.columnDef.meta?.className, cell.column.columnDef.meta?.tdClassName)}>
      {flexRender(cell.column.columnDef.cell, cell.getContext())}
    </TableCell>
  ))}
</TableRow>
```

- `row.id` 而非 `row.original.id`：尊重各页 `getRowId`，避免 persons/cameras 行对象没有 `id` 时退化。
- `data-state` 用三元而非 `&& 'selected'`：后者在未选中时输出 `data-state="false"`，会被 `data-[state=selected]` 之外的样式误伤。
- `className='group'` 是粘性操作列两层伪元素 hover 的宿主，`development-guidelines.md` §2.2 依赖它；`tdClassName` / `thClassName` 在 `web/src/tanstack-table.d.ts` 里已声明但此前无人消费，本变更让它们生效。

## 4. 列头对齐（改 `web/src/components/data-table/column-header.tsx`）

当前 `!column.getCanSort()` 分支返回裸 `<div>{title}</div>`，可排序分支返回 `h-8` ghost Button（`px-3`）。同一行里两者文本左边界相差 12px、高度相差 12px。

改为：

```tsx
if (!column.getCanSort()) {
  return (
    <div className={cn('relative -mx-3 flex h-8 items-center px-3 start-3', className)}>
      {title}
    </div>
  )
}
```

- 与 `Button size='sm'` 的 `h-8 px-3` 对齐；`TableHead` 的 `h-10 px-2` 不变，因此行高不变，只有文本内缩统一。
- `-mx-3` 抵消 `px-3` 的排版占位、`start-3` 只做视觉右移：`px-3` 会把表头单元格撑宽 24px，整表随之变宽，在 414px 视口下行中心会落到横向滚动容器之外，`locator.click()` 点到 `<html>` 上，「点行打开详情」随之失效（2.3 迁移时实测）。两者配合后排版宽度与迁移前一致，文字仍落在 20px 处。
- 复选框列、操作列的表头不是文本（`header` 返回 checkbox / `sr-only`），`px-3` 不影响其布局。
- 这是一次**有意的全局视觉对齐**：persons/audit 的不可排序表头会右移 12px，与 cameras/tasks/algorithms 的可排序表头对齐。

## 5. 判定条件（写入规格 §4.1）

| 条件 | 结论 |
| :--- | :--- |
| `manualPagination` + 后端 `total`（persons、audit） | 关闭全部列排序（`enableSorting: false`）；客户端排序只会排当前页，是错误语义 |
| 客户端分页（cameras、algorithms、tasks） | 可开列排序；前提是前端一次取全量且上限受控（cameras ≤ `MaxCameraCapacity` 128） |
| 有 grid 渲染分支（persons） | 工具栏右侧放 `ViewModeSwitch`，通过 `toolbarActions` 传入 |
| 工具栏有额外控件（tasks 的展开/收起） | 通过 `toolbarActions` 传入，不包 `DataTableToolbar` 的外壳 |
| 0 行 | `totalRows === 0`，分页条不渲染；空态放 `TableCell` 内或 `rounded-xl border border-dashed` 容器 |
| 页面不是 CRUD 列表（gb28181 设置、records 媒体工作台、image-search/live/spatial） | 例外白名单，不受 §4.1 约束 |

## 6. 迁移映射

| 页面 | 入口改动 | 表格改动 | `totalRows` |
| :--- | :--- | :--- | :--- |
| persons | 删除 `Header`/`Main`/顶栏四件套 import，改 `<ListPage title description actions={<PersonsPrimaryButtons/>}>`；`<div aria-busy>{...}` 保留在外层 | `PersonsTable` 改用 shell：`toolbar={<DataTableToolbar …/>}`、`toolbarActions={<ViewModeSwitch/>}`、`skeletonRows={pageSize}`、`loadingRow={<PersonCellSkeleton columns…/>}`、`emptyState={<PersonsTableEmptyRow/>}`、`errorState={<PersonsTableErrorRow/>}` | `data?.total ?? 0` |
| cameras | 同上，`actions={<CamerasPrimaryButtons/>}` | shell + 既有 `DataTableToolbar`；骨架/空态/错误行原样作为插槽 | `table.getFilteredRowModel().rows.length` |
| algorithms | 同上 | 同上 | 同上 |
| audit | 同上（`AuditToolbar` 作为 `toolbar`） | 同上 | `total`（manualPagination） |
| tasks | 同上 | `TasksTableDetail` 改用 shell：`renderRow={(row) => <><TableRow …/>{row.getIsExpanded() && <TableRow>…</TableRow>}</>}`、`skeletonRows={5}` | `table.getFilteredRowModel().rows.length` |

迁移后每个页面删除的重复代码：`<Header fixed>` + 四件套（约 10 行）、`<Main>` 包裹、标题行（约 12 行）、容器 div + `Table` + `TableHeader` 样板（约 20 行）。

## 7. 门禁设计

1. `web/src/components/list-page.test.tsx`：断言 `[data-slot='list-page-header']`、`list-page-heading` 的 `min-w-0 flex-1`、描述 `max-w-3xl`、`list-page-actions` 只在传 `actions` 时出现。渲染需要 `DirectionProvider` + `ThemeProvider` + `LayoutProvider` + `SidebarProvider` + `SearchProvider`（`Search` 依赖 `useSearch()`），套用 `config-drawer.test.tsx` 的 provider 组合。
2. `web/src/components/data-table/shell.test.tsx`：用 `useReactTable` 造 3 行假数据，断言容器类名精确匹配、`totalRows=0` 无分页条、`totalRows>0` 有分页条、loading 行数 = `skeletonRows`、error/empty 分支、`renderRow` 返回两行时 `tbody` 有 4 个 `tr`、`tdClassName` 生效。
3. `web/src/components/data-table/column-header.test.tsx`：一张表两列（一列 `enableSorting: false`），断言两个 `th` 内包裹层 `getBoundingClientRect().height` 相等且为 32，不可排序列包裹层含 `px-3`。
4. 5 个既有 `*-table.test.tsx` 各加一条 `expect(container.querySelector('[data-slot="data-table-container"]')).not.toBeNull()`。
5. `web/eslint.config.js` 追加一段只作用于 5 个入口文件的配置，`no-restricted-imports` 禁止 `@/components/layout/header` 与 `@/components/layout/main`。`defineConfig` 接受多段配置对象，用 `files` 精确限定，不影响 live/records/spatial/image-search。

## 8. 规格改写要点（§4.1）

- 标题从「照抄 persons / cameras」改为「必须使用 `ListPage` / `DataTableShell`」，条款表每行绑定到组件或 `data-slot`。
- 删除「视图切换与 persons 一致」这类以页面为参照的表述，改为「有 grid 分支时用 `<ViewModeSwitch/>` 走 `toolbarActions`」。
- 保留空态铁律与「按业务有效项判断」的段落，补一句「分页条由 `DataTableShell` 依据 `totalRows` 统一裁决」。
- 新增例外白名单小节：`gb28181`（设置页）、`records`（`RecordsListShell`）、`image-search` / `live` / `spatial`（工作台）。

## Technical Research & References

- TanStack Table v8 `Table` / `Row` / `flexRender` / `getVisibleLeafColumns` / `getFilteredRowModel`：<https://tanstack.com/table/v8/docs/api/core/table>、<https://tanstack.com/table/v8/docs/api/core/row>
- `ColumnMeta` 模块增强：仓库内 `web/src/tanstack-table.d.ts` 已声明 `className` / `tdClassName` / `thClassName` / `title`。
- ESLint `no-restricted-imports`（core 规则，按 `paths[].name` 精确匹配模块说明符）：<https://eslint.org/docs/latest/rules/no-restricted-imports>
- ESLint flat config 多配置对象与 `files`：<https://eslint.org/docs/latest/use/configure/configuration-files>
- 项目内既有契约：`.luoshu/specs/frontend/theme-and-i18n.md` §4.1、`.luoshu/specs/frontend/development-guidelines.md` §2.2、`.luoshu/rules/web/frontend-browser-tests`。

## Review Focus

1. **`renderRow` 与展开行**：tasks 的主行 + 展开行必须仍是 `tbody` 的兄弟 `tr`；若 shell 把 `renderRow` 结果包进额外 `<tr>` 会破坏 `colSpan` 与 `getIsExpanded` 的布局。
2. **`totalRows` 语义一致性**：persons/audit 传服务端 `total`，其余传 `getFilteredRowModel().rows.length`；传错会在「筛选后 0 行」时残留 `Page 1 of 0` 或反之隐藏分页。
3. **provider 依赖**：`ListPage` 渲染 `Search`，测试必须包 `SearchProvider`，否则 `useSearch()` 抛错；生产侧由 `AuthenticatedLayout` 提供，确认没有页面在 layout 之外直接渲染 `ListPage`。
4. **行选中态**：`data-state` 从 `false` 改为 `undefined` 后，确认批量操作栏、`data-[state=selected]` 样式与既有测试的选择器仍成立。
5. **粘性列**：`group` 类必须仍落在 `tr` 上，且 `meta.className` 的 `bg-card` 与两层伪元素 hover 不被 shell 的 `className` 覆盖顺序破坏（`cn` 合并顺序：容器类在前，`className` prop 在后）。
