import { useEffect, useState } from 'react'
import {
  type SortingState,
  type VisibilityState,
  flexRender,
  getCoreRowModel,
  getFacetedRowModel,
  getFacetedUniqueValues,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { AlertCircle, Camera as CameraIcon, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { type NavigateFn, useTableUrlState } from '@/hooks/use-table-url-state'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { DataTablePagination, DataTableToolbar } from '@/components/data-table'
import { type Camera } from '../data/schema'
import { camerasColumns as columns } from './cameras-columns'
import { CamerasDataTableBulkActions } from './cameras-data-table-bulk-actions'
import { useCamerasContext } from './cameras-provider'

interface CamerasTableProps {
  data: Camera[]
  total: number
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  search: Record<string, unknown>
  navigate: NavigateFn
}

export function CamerasTable({
  data,
  total,
  isLoading,
  isError,
  onRetry,
  search,
  navigate,
}: CamerasTableProps) {
  const { t } = useTranslation('cameras')
  const { setOpen } = useCamerasContext()

  const [rowSelection, setRowSelection] = useState({})
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>({})
  const [sorting, setSorting] = useState<SortingState>([])

  const {
    columnFilters,
    onColumnFiltersChange,
    pagination,
    onPaginationChange,
    globalFilter,
    onGlobalFilterChange,
    ensurePageInRange,
  } = useTableUrlState({
    search,
    navigate,
    pagination: { defaultPage: 1, defaultPageSize: 10 },
    globalFilter: { enabled: true, key: 'search' },
    columnFilters: [{ columnId: 'health', searchKey: 'status', type: 'array' }],
  })

  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable({
    data,
    columns,
    state: {
      sorting,
      columnVisibility,
      rowSelection,
      columnFilters,
      pagination,
      globalFilter,
    },
    enableRowSelection: true,
    onRowSelectionChange: setRowSelection,
    onSortingChange: setSorting,
    onColumnFiltersChange,
    onColumnVisibilityChange: setColumnVisibility,
    onPaginationChange,
    onGlobalFilterChange,
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFacetedRowModel: getFacetedRowModel(),
    getFacetedUniqueValues: getFacetedUniqueValues(),
    pageCount: Math.ceil(total / pagination.pageSize) || 1,
    manualPagination: true,
  })

  const pageCount = table.getPageCount()
  useEffect(() => {
    ensurePageInRange(pageCount)
  }, [pageCount, ensurePageInRange])

  const statusOptions = [
    { label: t('status.online'), value: 'online' },
    { label: t('status.offline'), value: 'offline' },
    { label: t('status.error'), value: 'error' },
    { label: t('status.unknown'), value: 'unknown' },
  ]

  const isFiltered =
    Boolean(globalFilter) || (columnFilters && columnFilters.length > 0)

  return (
    <div className='flex flex-1 flex-col gap-4'>
      <DataTableToolbar
        table={table}
        searchPlaceholder={t('table.searchPlaceholder')}
        filters={[
          {
            columnId: 'health',
            title: t('table.filterStatus'),
            options: statusOptions,
          },
        ]}
        columnLabels={{
          id: t('table.columns.id'),
          name: t('table.columns.name'),
          rtspUrl: t('table.columns.rtspUrl'),
          health: t('table.columns.health'),
          mainStream: t('table.columns.mainStream'),
          subStream: t('table.columns.subStream'),
          enabled: t('table.columns.enabled'),
          updatedAt: t('table.columns.updatedAt'),
        }}
      />

      <div className='rounded-md border border-border bg-card'>
        <Table className='border-separate border-spacing-0'>
          <TableHeader>
            {table.getHeaderGroups().map((headerGroup) => (
              <TableRow key={headerGroup.id}>
                {headerGroup.headers.map((header) => (
                  <TableHead
                    key={header.id}
                    colSpan={header.colSpan}
                    className={header.column.columnDef.meta?.className}
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
            {isLoading ? (
              // 骨架屏加载态
              Array.from({ length: 5 }).map((_, index) => (
                <TableRow key={`skeleton-${index}`}>
                  <TableCell className='w-12'>
                    <Skeleton className='h-4 w-4' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-4 w-24' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-4 w-32' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-4 w-44' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-6 w-16 rounded-full' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-4 w-28' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-4 w-24' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-5 w-8 rounded-full' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-4 w-24' />
                  </TableCell>
                  <TableCell className='sticky end-0 right-0 z-20 w-12 bg-card text-end'>
                    <Skeleton className='h-8 w-8 rounded-md' />
                  </TableCell>
                </TableRow>
              ))
            ) : isError ? (
              // 请求失败态
              <TableRow>
                <TableCell
                  colSpan={columns.length}
                  className='h-48 text-center'
                >
                  <div className='flex flex-col items-center justify-center gap-2 text-destructive'>
                    <AlertCircle className='h-8 w-8' />
                    <span className='font-medium'>
                      加载摄像机数据失败，请检查网络或后端服务
                    </span>
                    <Button
                      variant='outline'
                      size='sm'
                      onClick={onRetry}
                      className='mt-2'
                    >
                      <RefreshCw className='me-1 h-4 w-4' />
                      重试
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ) : table.getRowModel().rows?.length ? (
              table.getRowModel().rows.map((row) => (
                <TableRow
                  key={row.original.id}
                  data-state={row.getIsSelected() && 'selected'}
                  className='group'
                >
                  {row.getVisibleCells().map((cell) => (
                    <TableCell
                      key={cell.id}
                      className={cell.column.columnDef.meta?.className}
                    >
                      {flexRender(
                        cell.column.columnDef.cell,
                        cell.getContext()
                      )}
                    </TableCell>
                  ))}
                </TableRow>
              ))
            ) : (
              // 空数据态（区分无数据与筛选无匹配）
              <TableRow>
                <TableCell
                  colSpan={columns.length}
                  className='h-64 text-center'
                >
                  <div className='flex flex-col items-center justify-center gap-2'>
                    <div className='flex h-12 w-12 items-center justify-center rounded-full bg-muted'>
                      <CameraIcon className='h-6 w-6 text-muted-foreground' />
                    </div>
                    <span className='text-base font-semibold text-foreground'>
                      {isFiltered ? t('table.filterEmpty') : t('table.empty')}
                    </span>
                    <p className='max-w-sm text-sm text-muted-foreground'>
                      {isFiltered
                        ? t('table.filterEmptyDesc')
                        : t('table.emptyDesc')}
                    </p>
                    {isFiltered ? (
                      <Button
                        variant='outline'
                        size='sm'
                        onClick={() => {
                          table.resetGlobalFilter()
                          table.resetColumnFilters()
                        }}
                        className='mt-2'
                      >
                        {t('table.resetFilter')}
                      </Button>
                    ) : (
                      <Button
                        size='sm'
                        onClick={() => setOpen('create')}
                        className='mt-2'
                      >
                        {t('actions.create')}
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      <DataTablePagination table={table} />

      <CamerasDataTableBulkActions table={table} />
    </div>
  )
}
