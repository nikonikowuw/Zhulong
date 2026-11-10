import { useEffect, useMemo, useState } from 'react'
import {
  type RowSelectionState,
  type VisibilityState,
  flexRender,
  getCoreRowModel,
  getFacetedRowModel,
  getFacetedUniqueValues,
  getFilteredRowModel,
  getPaginationRowModel,
  useReactTable,
} from '@tanstack/react-table'
import {
  AlertCircle,
  LayoutGrid,
  List,
  RefreshCw,
  Users as UsersIcon,
} from 'lucide-react'
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
import {
  ViewModeSwitch,
  type ViewModeOption,
} from '@/components/view-mode-switch'
import { type Person } from '../data/schema'
import { personsColumns as columns } from './persons-columns'
import { PersonsDataTableBulkActions } from './persons-data-table-bulk-actions'
import { PersonsGrid } from './persons-grid'
import { usePersonsActions } from './persons-provider'

const viewModeOptions = [
  { value: 'table', label: 'views.table', icon: List },
  { value: 'grid', label: 'views.grid', icon: LayoutGrid },
] as const satisfies ReadonlyArray<ViewModeOption<'table' | 'grid'>>

/** 骨架屏行数。列数不再写死：列定义增长时硬编码的 5 个单元格会横向错位。 */
const SKELETON_ROWS = 5

interface PersonsTableProps {
  data: Person[]
  total: number
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  search: Record<string, unknown>
  navigate: NavigateFn
}

export function PersonsTable({
  data,
  total,
  isLoading,
  isError,
  onRetry,
  search,
  navigate,
}: PersonsTableProps) {
  const { t } = useTranslation('persons')
  const { setOpen } = usePersonsActions()

  const [rowSelection, setRowSelection] = useState<RowSelectionState>({})
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>({})
  // 双视角：档案表格（主视角） ⇋ 人脸画廊；缺省 `view` 即主视角，与任务页一致
  const viewMode: 'table' | 'grid' = search.view === 'grid' ? 'grid' : 'table'

  const handleViewModeChange = (mode: 'table' | 'grid') => {
    navigate({
      search: (prev) => ({
        ...prev,
        view: mode === 'grid' ? mode : undefined,
      }),
    })
  }

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
    pagination: { defaultPage: 1, defaultPageSize: 10, maxPageSize: 100 },
    globalFilter: { enabled: true, key: 'search' },
    columnFilters: [
      // 后端 `faceStatus` 只按单值过滤（enrolled / missing），因此这里用
      // string 类型的 faceted filter 而不是 array：多选再多，服务端也只认一个值。
      { columnId: 'faces', searchKey: 'faceStatus', type: 'string' },
    ],
  })

  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable({
    data,
    columns,
    state: {
      columnVisibility,
      rowSelection,
      columnFilters,
      pagination,
      globalFilter,
    },
    enableRowSelection: true,
    onRowSelectionChange: setRowSelection,
    onColumnFiltersChange,
    onColumnVisibilityChange: setColumnVisibility,
    // 换页即清空选择：批量删除只应作用于当前可见的勾选，否则用户在第 1 页勾
    // 了三个人、翻到第 3 页再点批量删除，会连带删掉早已滚出视野的记录。
    // 同时给每行一个稳定的业务 ID —— 默认 rowId 是数组下标，跨页会把上一页
    // 第 N 行的选中态「继承」给下一页第 N 行。
    onPaginationChange: (updater) => {
      setRowSelection({})
      onPaginationChange(updater)
    },
    onGlobalFilterChange,
    getRowId: (row) => row.id,
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    getFacetedRowModel: getFacetedRowModel(),
    getFacetedUniqueValues: getFacetedUniqueValues(),
    pageCount: Math.ceil(total / pagination.pageSize) || 1,
    manualPagination: true,
    manualFiltering: true,
  })

  const pageCount = table.getPageCount()
  useEffect(() => {
    ensurePageInRange(pageCount)
  }, [pageCount, ensurePageInRange])

  const isFiltered =
    Boolean(globalFilter) || (columnFilters && columnFilters.length > 0)

  const faceStatusOptions = useMemo(
    () => [
      { label: t('table.filterFaceStatus.enrolled'), value: 'enrolled' },
      { label: t('table.filterFaceStatus.missing'), value: 'missing' },
    ],
    [t]
  )

  const filters = useMemo(
    () => [
      {
        columnId: 'faces',
        title: t('table.filterFaceStatus.title'),
        options: faceStatusOptions,
        // 本地数据只有当前页，faceted 计数会让人误读成全库统计。
        showFacets: false,
        // 后端 `faceStatus` 只接受一个值：多选会被压成首值，界面却显示两项都生效。
        single: true,
      },
    ],
    [t, faceStatusOptions]
  )

  const columnLabels = useMemo(
    () => ({
      avatar: t('table.columns.avatar'),
      name: t('table.columns.name'),
      personId: t('table.columns.personId'),
      source: t('table.columns.source'),
      faces: t('table.columns.faceCount'),
      createdAt: t('table.columns.createdAt'),
    }),
    [t]
  )

  const viewModeSwitchOptions = useMemo(
    () =>
      viewModeOptions.map((option) => ({ ...option, label: t(option.label) })),
    [t]
  )

  const visibleColumnCount = table.getVisibleLeafColumns().length
  const rows = table.getRowModel().rows
  const isEmpty = !isLoading && !isError && rows.length === 0

  const handleResetFilters = () => {
    table.resetGlobalFilter()
    table.resetColumnFilters()
  }

  return (
    <div className='flex flex-1 flex-col gap-4'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div className='flex-1'>
          <DataTableToolbar
            table={table}
            searchPlaceholder={t('table.searchPlaceholder')}
            filters={filters}
            columnLabels={columnLabels}
            debounceMs={300}
          />
        </div>
        <ViewModeSwitch
          value={viewMode}
          onChange={handleViewModeChange}
          options={viewModeSwitchOptions}
        />
      </div>

      {viewMode === 'table' ? (
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
                Array.from({ length: SKELETON_ROWS }).map((_, index) => (
                  <TableRow key={`skeleton-${index}`}>
                    {table.getVisibleLeafColumns().map((column) => (
                      <TableCell
                        key={`skeleton-${index}-${column.id}`}
                        className={column.columnDef.meta?.className}
                      >
                        <Skeleton className='h-4 w-full max-w-28' />
                      </TableCell>
                    ))}
                  </TableRow>
                ))
              ) : isError ? (
                <TableRow>
                  <TableCell
                    colSpan={visibleColumnCount}
                    className='h-48 text-center'
                  >
                    <div className='flex flex-col items-center justify-center gap-2 text-destructive'>
                      <AlertCircle className='h-8 w-8' />
                      <span className='font-medium'>
                        {t('table.loadFailed')}
                      </span>
                      <Button
                        variant='outline'
                        size='sm'
                        onClick={onRetry}
                        className='mt-2'
                      >
                        <RefreshCw className='me-1 h-4 w-4' />
                        {t('actions.retry')}
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ) : rows.length > 0 ? (
                rows.map((row) => (
                  <TableRow
                    key={row.id}
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
                <TableRow>
                  <TableCell
                    colSpan={visibleColumnCount}
                    className='h-64 text-center'
                  >
                    <div className='flex flex-col items-center justify-center gap-2'>
                      <div className='flex h-12 w-12 items-center justify-center rounded-full bg-muted'>
                        <UsersIcon className='h-6 w-6 text-muted-foreground' />
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
                          onClick={handleResetFilters}
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
      ) : (
        <PersonsGrid
          table={table}
          isLoading={isLoading}
          isError={isError}
          onRetry={onRetry}
          isFiltered={isFiltered}
          onResetFilters={handleResetFilters}
        />
      )}

      {/* 空态不渲染分页条：`Page 1 of 0` / `Page 1 of 1` 都是纯噪音。 */}
      {!isEmpty && <DataTablePagination table={table} />}

      <PersonsDataTableBulkActions table={table} />
    </div>
  )
}
