import { useEffect, useMemo, useState } from 'react'
import {
  type RowSelectionState,
  type VisibilityState,
  flexRender,
  getCoreRowModel,
  getPaginationRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { LayoutGrid, List } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { type NavigateFn, useTableUrlState } from '@/hooks/use-table-url-state'
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
import {
  PersonCellSkeleton,
  PersonsTableEmptyRow,
  PersonsTableErrorRow,
} from './persons-table-states'

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
    // 换页即清空选择，并使用业务 ID 作为行标识，避免跨页选中态串行。
    onPaginationChange: (updater) => {
      setRowSelection({})
      onPaginationChange(updater)
    },
    onGlobalFilterChange,
    getRowId: (row) => row.id,
    getCoreRowModel: getCoreRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    pageCount: Math.max(1, Math.ceil(total / pagination.pageSize)),
    manualPagination: true,
    manualFiltering: true,
    manualSorting: true,
  })

  const pageCount = table.getPageCount()
  const canCheckPageRange = !isLoading && total > 0
  useEffect(() => {
    if (canCheckPageRange) {
      ensurePageInRange(pageCount)
    }
  }, [canCheckPageRange, pageCount, ensurePageInRange])

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
        showFacets: false,
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
                        <PersonCellSkeleton columnId={column.id} />
                      </TableCell>
                    ))}
                  </TableRow>
                ))
              ) : isError ? (
                <PersonsTableErrorRow
                  colSpan={visibleColumnCount}
                  onRetry={onRetry}
                />
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
                <PersonsTableEmptyRow
                  colSpan={visibleColumnCount}
                  isFiltered={isFiltered}
                  onResetFilters={handleResetFilters}
                  onCreate={() => setOpen('create')}
                />
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
