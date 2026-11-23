import { Fragment } from 'react'
import {
  flexRender,
  type Row,
  type Table as TanstackTable,
} from '@tanstack/react-table'
import { cn } from '@/lib/utils'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { DataTablePagination } from './pagination'

export type DataTableShellProps<TData> = {
  table: TanstackTable<TData>
  /** 工具栏主体，通常是 <DataTableToolbar /> */
  toolbar?: React.ReactNode
  /** 工具栏右侧附加控件，如 view 切换 */
  toolbarActions?: React.ReactNode
  /** 过滤后、分页前的总行数；为 0 时不渲染分页条 */
  totalRows: number
  isLoading?: boolean
  isError?: boolean
  /** 骨架行数，默认 5；服务端分页页应传当前 pageSize */
  skeletonRows?: number
  /** 单行骨架；缺省按可见列数渲染通用骨架 */
  loadingRow?: React.ReactNode
  /** 必须是 <TableRow>，colSpan 由调用方按自己的列数给 */
  emptyState?: React.ReactNode
  errorState?: React.ReactNode
  /** 逃生口：需要「主行 + 展开行」时返回一个含多行的 Fragment */
  renderRow?: (row: Row<TData>) => React.ReactNode
  /** 覆盖默认分页槽 */
  footer?: React.ReactNode
  className?: string
}

/**
 * 列表页表格骨架：工具栏行 + 容器 + 四态表体 + 分页。
 *
 * 抽出来的动机不是复用样式，而是让「容器 token、四态优先级、空态不出分页条」
 * 从评审口口相传变成组件行为——`totalRows === 0` 时任何页面都无法再渲染出
 * 「Page 1 of 0」。列宽等页面差异走 columnDef.meta，而不是各自复制容器类名。
 */
export function DataTableShell<TData>({
  table,
  toolbar,
  toolbarActions,
  totalRows,
  isLoading = false,
  isError = false,
  skeletonRows = 5,
  loadingRow,
  emptyState,
  errorState,
  renderRow,
  footer,
  className,
}: DataTableShellProps<TData>): React.JSX.Element {
  const rows = table.getRowModel().rows
  const columnCount = table.getVisibleLeafColumns().length

  return (
    <div className='flex flex-1 flex-col gap-4'>
      <DataTableToolbarRow toolbar={toolbar} actions={toolbarActions} />

      <div
        data-slot='data-table-container'
        className={cn('rounded-md border border-border bg-card', className)}
      >
        <Table className='border-separate border-spacing-0'>
          <TableHeader>
            {table.getHeaderGroups().map((headerGroup) => (
              <TableRow key={headerGroup.id}>
                {headerGroup.headers.map((header) => (
                  <TableHead
                    key={header.id}
                    colSpan={header.colSpan}
                    className={cn(
                      header.column.columnDef.meta?.className,
                      header.column.columnDef.meta?.thClassName
                    )}
                  >
                    {header.isPlaceholder
                      ? null
                      : flexRender(
                          header.column.columnDef.header,
                          header.getContext()
                        )}
                  </TableHead>
                ))}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {isLoading
              ? Array.from(
                  { length: Math.max(skeletonRows, 1) },
                  (_, index) => (
                    <Fragment key={`skeleton-${index}`}>
                      {loadingRow ?? (
                        <DefaultSkeletonRow columnCount={columnCount} />
                      )}
                    </Fragment>
                  )
                )
              : isError
                ? errorState
                : rows.length > 0
                  ? rows.map((row) =>
                      renderRow ? (
                        <Fragment key={row.id}>{renderRow(row)}</Fragment>
                      ) : (
                        <DefaultRow key={row.id} row={row} />
                      )
                    )
                  : emptyState}
          </TableBody>
        </Table>
      </div>

      {footer ??
        (totalRows > 0 ? (
          <DataTablePagination table={table} total={totalRows} />
        ) : null)}
    </div>
  )
}

/**
 * 工具栏行。独立导出，供有 grid 分支的页面在非表格视图下复用同一条骨架，
 * 避免「切到画廊后工具栏换一套间距」。
 */
export function DataTableToolbarRow({
  toolbar,
  actions,
}: {
  toolbar?: React.ReactNode
  actions?: React.ReactNode
}): React.JSX.Element | null {
  if (!toolbar && !actions) {
    return null
  }

  return (
    <div
      data-slot='data-table-toolbar'
      className='flex flex-wrap items-center justify-between gap-2'
    >
      <div className='flex-1'>{toolbar}</div>
      {actions}
    </div>
  )
}

function DefaultRow<TData>({ row }: { row: Row<TData> }) {
  return (
    <TableRow
      // 未选中时不能输出 data-state='false'：会被 data-[state=selected] 之外的样式误伤。
      data-state={row.getIsSelected() ? 'selected' : undefined}
      // group 是粘性操作列两层伪元素 hover 的宿主，见 development-guidelines §2.2。
      className='group'
    >
      {row.getVisibleCells().map((cell) => (
        <TableCell
          key={cell.id}
          className={cn(
            cell.column.columnDef.meta?.className,
            cell.column.columnDef.meta?.tdClassName
          )}
        >
          {flexRender(cell.column.columnDef.cell, cell.getContext())}
        </TableCell>
      ))}
    </TableRow>
  )
}

function DefaultSkeletonRow({ columnCount }: { columnCount: number }) {
  return (
    <TableRow>
      {Array.from({ length: columnCount }, (_, index) => (
        <TableCell key={index}>
          <Skeleton className='h-4 w-full' />
        </TableCell>
      ))}
    </TableRow>
  )
}
