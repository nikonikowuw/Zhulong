import { useEffect, useMemo, useRef, useState } from 'react'
import {
  type SortingState,
  type VisibilityState,
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
import { TableCell, TableRow } from '@/components/ui/table'
import { DataTableShell, DataTableToolbar } from '@/components/data-table'
import { type Camera } from '../data/schema'
import { camerasColumns as columns } from './cameras-columns'
import { CamerasDataTableBulkActions } from './cameras-data-table-bulk-actions'
import { useCamerasActions } from './cameras-provider'

interface CamerasTableProps {
  data: Camera[]
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  search: Record<string, unknown>
  navigate: NavigateFn
}

const SKELETON_ROW = (
  <TableRow>
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
    <TableCell className='sticky inset-e-0 z-20 w-12 bg-card text-end'>
      <Skeleton className='size-8 rounded-md' />
    </TableCell>
  </TableRow>
)

export function CamerasTable({
  data,
  isLoading,
  isError,
  onRetry,
  search,
  navigate,
}: CamerasTableProps) {
  const { t } = useTranslation('cameras')
  const { setOpen } = useCamerasActions()

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
    getRowId: (row) => row.id,
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
  })

  const pageCount = table.getPageCount()
  const ensurePageInRangeRef = useRef(ensurePageInRange)
  useEffect(() => {
    ensurePageInRangeRef.current = ensurePageInRange
  }, [ensurePageInRange])
  useEffect(() => {
    ensurePageInRangeRef.current(pageCount)
  }, [pageCount, pagination.pageIndex])

  const statusOptions = useMemo(
    () => [
      { label: t('status.online'), value: 'online' },
      { label: t('status.offline'), value: 'offline' },
      { label: t('status.error'), value: 'error' },
      { label: t('status.unknown'), value: 'unknown' },
    ],
    [t]
  )

  const filters = useMemo(
    () => [
      {
        columnId: 'health',
        title: t('table.filterStatus'),
        options: statusOptions,
      },
    ],
    [t, statusOptions]
  )

  const columnLabels = useMemo(
    () => ({
      id: t('table.columns.id'),
      name: t('table.columns.name'),
      rtspUrl: t('table.columns.rtspUrl'),
      health: t('table.columns.health'),
      mainStream: t('table.columns.mainStream'),
      subStream: t('table.columns.subStream'),
      enabled: t('table.columns.enabled'),
      updatedAt: t('table.columns.updatedAt'),
    }),
    [t]
  )

  const isFiltered =
    Boolean(globalFilter) || (columnFilters && columnFilters.length > 0)

  return (
    <div className='flex flex-1 flex-col gap-4'>
      <DataTableShell
        table={table}
        toolbar={
          <DataTableToolbar
            table={table}
            searchPlaceholder={t('table.searchPlaceholder')}
            filters={filters}
            columnLabels={columnLabels}
          />
        }
        totalRows={table.getFilteredRowModel().rows.length}
        isLoading={isLoading}
        isError={isError}
        skeletonRows={5}
        loadingRow={SKELETON_ROW}
        errorState={
          // 请求失败态
          <TableRow>
            <TableCell colSpan={columns.length} className='h-48 text-center'>
              <div className='flex flex-col items-center justify-center gap-2 text-destructive'>
                <AlertCircle className='size-8' />
                <span className='font-medium'>{t('table.loadError')}</span>
                <Button
                  variant='outline'
                  size='sm'
                  onClick={onRetry}
                  className='mt-2'
                >
                  <RefreshCw className='me-1 size-4' />
                  {t('table.retry')}
                </Button>
              </div>
            </TableCell>
          </TableRow>
        }
        emptyState={
          // 空数据态（区分无数据与筛选无匹配）
          <TableRow>
            <TableCell colSpan={columns.length} className='h-64 text-center'>
              <div className='flex flex-col items-center justify-center gap-2'>
                <div className='flex size-12 items-center justify-center rounded-full bg-muted'>
                  <CameraIcon className='size-6 text-muted-foreground' />
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
        }
      />

      <CamerasDataTableBulkActions table={table} />
    </div>
  )
}
