import { useEffect, useMemo, useState } from 'react'
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
import { AlertCircle, Plus, RefreshCw, ServerOff } from 'lucide-react'
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
import { type Gb28181Device } from '../data/schema'
import { createGb28181DeviceColumns } from './gb28181-devices-columns'

interface Gb28181DevicesTableProps {
  devices: Gb28181Device[]
  isLoading: boolean
  isError: boolean
  errorMessage?: string
  onRetry: () => void
  onAdd?: () => void
  onEdit: (device: Gb28181Device) => void
  onViewChannels: (device: Gb28181Device) => void
  onDelete: (device: Gb28181Device) => void
  search: Record<string, unknown>
  navigate: NavigateFn
}

const SKELETON_ROWS = 3

/**
 * 设备 allowlist 表格。
 *
 * 分页与筛选走 URL（刷新/回退不丢状态），但分页在客户端完成：
 * 后端明确不分页（单机上限数百台），再加服务端分页只会多一层查询。
 */
export function Gb28181DevicesTable({
  devices,
  isLoading,
  isError,
  errorMessage,
  onRetry,
  onAdd,
  onEdit,
  onViewChannels,
  onDelete,
  search,
  navigate,
}: Gb28181DevicesTableProps) {
  const { t, i18n } = useTranslation('gb28181')

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
    columnFilters: [{ columnId: 'state', searchKey: 'status', type: 'array' }],
  })

  const columns = useMemo(
    () =>
      createGb28181DeviceColumns({
        t,
        language: i18n.language,
        onEdit,
        onViewChannels,
        onDelete,
      }),
    [t, i18n.language, onEdit, onViewChannels, onDelete]
  )

  const [sorting, setSorting] = useState<SortingState>([])
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>({})

  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable({
    data: devices,
    columns,
    state: {
      sorting,
      columnVisibility,
      columnFilters,
      pagination,
      globalFilter,
    },
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
  })

  const pageCount = table.getPageCount()
  useEffect(() => {
    ensurePageInRange(pageCount)
  }, [pageCount, ensurePageInRange])

  const isFiltered =
    Boolean(globalFilter) || (columnFilters && columnFilters.length > 0)
  const columnCount = columns.length
  // 0 行不渲染分页条：否则空态会多出一行「第 1 页，共 0 页」和每页行数选择器
  // （theme-and-i18n.md §4.1 空态铁律）。
  const hasRows = table.getFilteredRowModel().rows.length > 0

  return (
    <div className='flex flex-1 flex-col gap-3'>
      <DataTableToolbar
        table={table}
        searchPlaceholder={t('devices.searchPlaceholder')}
        debounceMs={150}
        filters={[
          {
            columnId: 'state',
            title: t('devices.filterState'),
            options: [
              { label: t('deviceState.online'), value: 'online' },
              { label: t('deviceState.offline'), value: 'offline' },
            ],
          },
        ]}
        columnLabels={{
          deviceId: t('devices.columns.deviceId'),
          name: t('devices.columns.name'),
          transport: t('devices.columns.transport'),
          state: t('devices.columns.state'),
          remoteAddr: t('devices.columns.remoteAddr'),
          expiresSeconds: t('devices.columns.expires'),
          lastKeepaliveAt: t('devices.columns.lastKeepalive'),
        }}
      />

      <div className='overflow-hidden rounded-xl border border-border/80 bg-card/75 shadow-2xs'>
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
              Array.from({ length: SKELETON_ROWS }).map((_, index) => (
                <TableRow key={`device-skeleton-${index}`}>
                  <TableCell>
                    <Skeleton className='h-4 w-40' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-4 w-24' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-4 w-10' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-6 w-16 rounded-full' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-4 w-32' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-4 w-16' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-4 w-28' />
                  </TableCell>
                  <TableCell className='sticky inset-e-0 z-10 w-12 bg-card text-end'>
                    <Skeleton className='size-8 rounded-md' />
                  </TableCell>
                </TableRow>
              ))
            ) : isError ? (
              <TableRow>
                <TableCell colSpan={columnCount} className='h-40 text-center'>
                  <div className='flex flex-col items-center justify-center gap-2 text-destructive'>
                    <AlertCircle className='size-7' />
                    <span className='text-sm font-medium'>
                      {t('devices.loadError')}
                    </span>
                    <span className='text-xs'>{errorMessage}</span>
                    <Button
                      variant='outline'
                      size='sm'
                      onClick={onRetry}
                      className='mt-1'
                    >
                      <RefreshCw className='me-1 size-3.5' />
                      {t('actions.retry')}
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ) : hasRows ? (
              table.getRowModel().rows.map((row) => (
                <TableRow
                  key={row.original.deviceId}
                  className='group'
                  data-state={row.getIsSelected() && 'selected'}
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
              <TableRow>
                <TableCell colSpan={columnCount} className='h-48 text-center'>
                  <div className='flex flex-col items-center justify-center gap-2'>
                    <div className='flex size-11 items-center justify-center rounded-full bg-muted'>
                      <ServerOff className='size-5 text-muted-foreground' />
                    </div>
                    <span className='text-sm font-semibold'>
                      {isFiltered
                        ? t('devices.filterEmpty')
                        : t('devices.empty')}
                    </span>
                    <p className='max-w-sm text-xs text-muted-foreground'>
                      {isFiltered
                        ? t('devices.filterEmptyDesc')
                        : t('devices.emptyDesc')}
                    </p>
                    {onAdd && !isFiltered && (
                      <Button
                        size='sm'
                        onClick={onAdd}
                        className='mt-2 h-8 gap-1.5 text-xs shadow-2xs'
                      >
                        <Plus className='size-3.5' />
                        {t('devices.addTitle')}
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      {hasRows && <DataTablePagination table={table} />}
    </div>
  )
}
