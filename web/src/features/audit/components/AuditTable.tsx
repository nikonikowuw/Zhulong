import { useEffect, useState } from 'react'
import {
  type SortingState,
  type VisibilityState,
  flexRender,
  getCoreRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { AlertCircle, RefreshCw, ScrollText } from 'lucide-react'
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
import { DataTablePagination } from '@/components/data-table'
import { type AuditLog } from '../data/schema'
import { type AuditDateRange, type TimePresetKey } from '../data/time-presets'
import { auditColumns } from './AuditColumns'
import { useAuditContext } from './AuditProvider'
import { AuditToolbar } from './AuditToolbar'

const auditSkeletonRows = [
  'skeleton-row-1',
  'skeleton-row-2',
  'skeleton-row-3',
  'skeleton-row-4',
  'skeleton-row-5',
  'skeleton-row-6',
]

interface AuditTableProps {
  data: AuditLog[]
  total: number
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  search: Record<string, unknown>
  navigate: NavigateFn
  preset?: TimePresetKey
  startTime?: string
  endTime?: string
  onDateRangeChange: (params: AuditDateRange) => void
}

export function AuditTable({
  data,
  total,
  isLoading,
  isError,
  onRetry,
  search,
  navigate,
  preset,
  startTime,
  endTime,
  onDateRangeChange,
}: AuditTableProps) {
  const { t } = useTranslation('audit')
  const { openDetail } = useAuditContext()

  const [rowSelection, setRowSelection] = useState({})
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>({})
  const [sorting, setSorting] = useState<SortingState>([])

  const {
    columnFilters,
    onColumnFiltersChange,
    pagination,
    onPaginationChange,
    ensurePageInRange,
  } = useTableUrlState({
    search,
    navigate,
    pagination: { defaultPage: 1, defaultPageSize: 20 },
    columnFilters: [
      { columnId: 'action', searchKey: 'action', type: 'array' },
      { columnId: 'status', searchKey: 'status', type: 'array' },
    ],
  })

  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable({
    data,
    columns: auditColumns,
    state: {
      sorting,
      columnVisibility,
      rowSelection,
      columnFilters,
      pagination,
    },
    enableRowSelection: false,
    onRowSelectionChange: setRowSelection,
    onSortingChange: setSorting,
    onColumnFiltersChange,
    onColumnVisibilityChange: setColumnVisibility,
    onPaginationChange,
    getCoreRowModel: getCoreRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getRowId: (row) => String(row.id),
    pageCount: Math.ceil(total / pagination.pageSize) || 1,
    manualFiltering: true,
    manualPagination: true,
  })

  useEffect(() => {
    const pageCount = Math.ceil(total / pagination.pageSize) || 1
    ensurePageInRange(pageCount)
  }, [total, pagination.pageSize, ensurePageInRange])

  const actionOptions = [
    { label: t('actionNames.auth.login'), value: 'auth.login' },
    { label: t('actionNames.auth.logout'), value: 'auth.logout' },
    { label: t('actionNames.auth.init'), value: 'auth.init' },
    { label: t('actionNames.camera.create'), value: 'camera.create' },
    { label: t('actionNames.camera.update'), value: 'camera.update' },
    { label: t('actionNames.camera.delete'), value: 'camera.delete' },
    { label: t('actionNames.camera.toggle'), value: 'camera.toggle' },
    {
      label: t('actionNames.storage.update_config'),
      value: 'storage.update_config',
    },
    {
      label: t('actionNames.storage.manual_cleanup'),
      value: 'storage.manual_cleanup',
    },
    {
      label: t('actionNames.storage.path_test'),
      value: 'storage.path_test',
    },
    {
      label: t('actionNames.storage.emergency_stop'),
      value: 'storage.emergency_stop',
    },
    {
      label: t('actionNames.storage.emergency_resume'),
      value: 'storage.emergency_resume',
    },
    {
      label: t('actionNames.time.update_config'),
      value: 'time.update_config',
    },
    { label: t('actionNames.time.ntp_sync'), value: 'time.ntp_sync' },
    { label: t('actionNames.time.manual_set'), value: 'time.manual_set' },
    { label: t('actionNames.time.rtc_heal'), value: 'time.rtc_heal' },
  ]

  const statusOptions = [
    { label: t('status.success'), value: 'success' },
    { label: t('status.failed'), value: 'failed' },
  ]

  return (
    <div className='flex flex-1 flex-col gap-4'>
      <AuditToolbar
        table={table}
        preset={preset}
        startTime={startTime}
        endTime={endTime}
        onDateRangeChange={onDateRangeChange}
        actionOptions={actionOptions}
        statusOptions={statusOptions}
        columnLabels={{
          id: t('table.columns.id'),
          createdAt: t('table.columns.createdAt'),
          username: t('table.columns.username'),
          action: t('table.columns.action'),
          target: t('table.columns.target'),
          ip: t('table.columns.ip'),
          status: t('table.columns.status'),
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
              auditSkeletonRows.map((skeletonRow) => (
                <TableRow key={skeletonRow}>
                  <TableCell className='py-3'>
                    <Skeleton className='h-4 w-12' />
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
                  <TableCell>
                    <Skeleton className='h-4 w-20' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-4 w-24' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-5 w-16 rounded-full' />
                  </TableCell>
                  <TableCell className='text-end'>
                    <Skeleton className='ms-auto h-7 w-16' />
                  </TableCell>
                </TableRow>
              ))
            ) : isError ? (
              <TableRow>
                <TableCell
                  colSpan={auditColumns.length}
                  className='h-48 text-center'
                >
                  <div className='flex flex-col items-center justify-center gap-2'>
                    <AlertCircle className='h-8 w-8 text-destructive' />
                    <p className='text-sm font-medium text-destructive'>
                      {t('table.error')}
                    </p>
                    <Button
                      variant='outline'
                      size='sm'
                      onClick={onRetry}
                      className='mt-2 gap-1.5'
                    >
                      <RefreshCw className='h-3.5 w-3.5' />
                      {t('table.retry')}
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ) : table.getRowModel().rows?.length ? (
              table.getRowModel().rows.map((row) => (
                <TableRow
                  key={row.id}
                  data-state={row.getIsSelected() && 'selected'}
                  className='cursor-pointer transition-colors hover:bg-muted/50'
                  onClick={() => openDetail(row.original)}
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
                <TableCell
                  colSpan={auditColumns.length}
                  className='h-48 text-center'
                >
                  <div className='flex flex-col items-center justify-center gap-2 text-muted-foreground'>
                    <ScrollText className='h-8 w-8 opacity-40' />
                    <p className='text-sm font-medium text-foreground'>
                      {t('table.empty')}
                    </p>
                    <p className='text-xs'>{t('table.emptyDesc')}</p>
                  </div>
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      <DataTablePagination table={table} />
    </div>
  )
}
