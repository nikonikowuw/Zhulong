import { useEffect, useMemo, useState } from 'react'
import {
  type Table as TanstackTable,
  type VisibilityState,
  flexRender,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { AlertCircle, RefreshCw, ScrollText } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { type NavigateFn, useTableUrlState } from '@/hooks/use-table-url-state'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { TableCell, TableRow } from '@/components/ui/table'
import { DataTableShell } from '@/components/data-table'
import { AUDIT_ACTION_KEYS } from '../data/audit-actions'
import { type AuditLog } from '../data/schema'
import { type AuditDateRange, type TimePresetKey } from '../data/time-presets'
import { auditColumns } from './AuditColumns'
import { useAuditContext } from './AuditProvider'
import { AuditToolbar } from './AuditToolbar'

const AUDIT_SKELETON_ROWS = 6

/** 骨架列宽按列 id 区分：状态是胶囊、操作是图标按钮，通用骨架会明显偏宽。 */
function AuditSkeletonRow({ table }: { table: TanstackTable<AuditLog> }) {
  return (
    <TableRow className='group'>
      {table.getVisibleLeafColumns().map((column) => (
        <TableCell key={column.id} className={column.columnDef.meta?.className}>
          <Skeleton
            className={
              column.id === 'actions'
                ? 'ms-auto h-7 w-8'
                : column.id === 'status'
                  ? 'h-5 w-16 rounded-full'
                  : 'h-4 w-full max-w-24'
            }
          />
        </TableCell>
      ))}
    </TableRow>
  )
}

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

  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>({})

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
      columnVisibility,
      columnFilters,
      pagination,
    },
    enableRowSelection: false,
    onColumnFiltersChange,
    onColumnVisibilityChange: setColumnVisibility,
    onPaginationChange,
    getCoreRowModel: getCoreRowModel(),
    getRowId: (row) => String(row.id),
    pageCount: Math.ceil(total / pagination.pageSize) || 1,
    manualFiltering: true,
    manualPagination: true,
  })

  useEffect(() => {
    const pageCount = Math.ceil(total / pagination.pageSize) || 1
    ensurePageInRange(pageCount)
  }, [total, pagination.pageSize, ensurePageInRange])

  const actionOptions = useMemo(
    () =>
      AUDIT_ACTION_KEYS.map((action) => ({
        label: t(`actionNames.${action}`),
        value: action,
      })),
    [t]
  )

  const statusOptions = useMemo(
    () => [
      { label: t('status.success'), value: 'success' },
      { label: t('status.failed'), value: 'failed' },
    ],
    [t]
  )

  return (
    <DataTableShell
      table={table}
      totalRows={total}
      isLoading={isLoading}
      isError={isError}
      skeletonRows={AUDIT_SKELETON_ROWS}
      loadingRow={<AuditSkeletonRow table={table} />}
      toolbar={
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
      }
      errorState={
        <TableRow>
          <TableCell colSpan={auditColumns.length} className='h-48 text-center'>
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
      }
      emptyState={
        <TableRow>
          <TableCell colSpan={auditColumns.length} className='h-48 text-center'>
            <div className='flex flex-col items-center justify-center gap-2 text-muted-foreground'>
              <ScrollText className='h-8 w-8 opacity-40' />
              <p className='text-sm font-medium text-foreground'>
                {t('table.empty')}
              </p>
              <p className='text-xs'>{t('table.emptyDesc')}</p>
            </div>
          </TableCell>
        </TableRow>
      }
      renderRow={(row) => (
        <TableRow
          data-state={row.getIsSelected() ? 'selected' : undefined}
          className='group cursor-pointer transition-colors hover:bg-muted/50'
          onClick={() => openDetail(row.original)}
        >
          {row.getVisibleCells().map((cell) => (
            <TableCell
              key={cell.id}
              className={cell.column.columnDef.meta?.className}
            >
              {flexRender(cell.column.columnDef.cell, cell.getContext())}
            </TableCell>
          ))}
        </TableRow>
      )}
    />
  )
}
