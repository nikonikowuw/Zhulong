import { useEffect, useMemo, useState } from 'react'
import {
  type RowSelectionState,
  type VisibilityState,
  flexRender,
  getCoreRowModel,
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

/**
 * 按列 id 匹配真实单元格形状的骨架。
 *
 * 统一一根 `h-4` 横条在加载完成时会造成可见的布局跳变：勾选框只有 16px、
 * 头像是 36px 圆、操作列是 32px 方形按钮。形状对不上的骨架屏不只是难看，
 * 它把「正在加载」和「数据到了」两帧的列宽也拉得不一致。
 */
function CellSkeleton({ columnId }: { columnId: string }) {
  switch (columnId) {
    case 'select':
      return <Skeleton className='mx-auto h-4 w-4 rounded-sm' />
    case 'avatar':
      return <Skeleton className='mx-auto h-9 w-9 rounded-full' />
    case 'faces':
      return <Skeleton className='h-7 w-20 rounded-md' />
    case 'actions':
      return <Skeleton className='ms-auto h-8 w-8 rounded-md' />
    case 'source':
      return <Skeleton className='h-5 w-16 rounded-full' />
    case 'createdAt':
      return <Skeleton className='h-4 w-30' />
    default:
      return <Skeleton className='h-4 w-28' />
  }
}

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
    getPaginationRowModel: getPaginationRowModel(),
    // `manualFiltering: true` 时不要挂 `getFilteredRowModel` /
    // `getFacetedRowModel` / `getFacetedUniqueValues`：它们只会拿当前页数据在
    // 本地再跑一遍过滤 —— 既不参与渲染（`showFacets: false`），又白白浪费
    // 一次全表扫描，还会让人误以为那个计数是全库统计。
    // 分页/排序同理都由 URL + 后端承担，因此也删掉 `getSortedRowModel`。
    pageCount: Math.max(1, Math.ceil(total / pagination.pageSize)),
    manualPagination: true,
    manualFiltering: true,
    manualSorting: true,
  })

  // 首屏 `total` 还是 0 时 `pageCount === 1`，此时下降回第 1 页会把深链接
  // （分享的 `?page=3`、刷新页面）抹掉，而且数据到货后也回不去 —— 必须等真实
  // 总数到货后才执行夹紧。
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
                        {/* 按列渲染对应形状的骨架构件：全渲染成同一根横条会在
                            数据到货时整表跳变（checkbox / 头像 / 操作按钮
                            的形状与尺寸都不一样）。 */}
                        <CellSkeleton columnId={column.id} />
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
