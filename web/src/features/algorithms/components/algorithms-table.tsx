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
import { AlertCircle, Layers, RefreshCw, UploadCloud } from 'lucide-react'
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
import { type Algorithm } from '../data/schema'
import { algorithmsColumns as columns } from './algorithms-columns'
import { useAlgorithmsContext } from './algorithms-provider'

interface AlgorithmsTableProps {
  data: Algorithm[]
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  search: Record<string, unknown>
  navigate: NavigateFn
  onOpenUpload?: () => void
}

export function AlgorithmsTable({
  data,
  isLoading,
  isError,
  onRetry,
  search,
  navigate,
  onOpenUpload,
}: AlgorithmsTableProps): React.JSX.Element {
  const { t } = useTranslation('algorithms')
  const { setOpen } = useAlgorithmsContext()

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
    columnFilters: [
      { columnId: 'taskType', searchKey: 'taskType', type: 'array' },
      { columnId: 'status', searchKey: 'status', type: 'array' },
    ],
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
  })

  const pageCount = table.getPageCount()
  useEffect(() => {
    ensurePageInRange(pageCount)
  }, [pageCount, ensurePageInRange])

  const taskTypeOptions = [
    { label: t('taskDetection', '目标检测'), value: 'detection' },
    { label: t('taskClassification', '图像分类'), value: 'classification' },
    {
      label: t('taskPersonStructure', '结构化人脸'),
      value: 'person_structure',
    },
    {
      label: t('taskVehicleStructure', '机动车结构化'),
      value: 'vehicle_structure',
    },
  ]

  const statusOptions = [
    { label: t('statusActive', '已激活'), value: 'ACTIVE' },
    { label: t('statusRunning', '运行中'), value: 'RUNNING' },
    { label: t('statusStarting', '启动中'), value: 'STARTING' },
    { label: t('statusDisabled', '已停用'), value: 'DISABLED' },
    { label: t('statusCrashed', '崩溃'), value: 'CRASHED' },
    { label: t('statusCrashLoop', '反复崩溃'), value: 'CRASH_LOOP' },
    { label: t('statusRollingBack', '回滚中'), value: 'ROLLING_BACK' },
  ]

  const isFiltered =
    Boolean(globalFilter) || (columnFilters && columnFilters.length > 0)

  const handleOpenUpload = () => {
    if (onOpenUpload) {
      onOpenUpload()
    } else {
      setOpen('upload')
    }
  }

  return (
    <div className='flex flex-1 flex-col gap-4'>
      <DataTableToolbar
        table={table}
        searchPlaceholder={t('searchPlaceholder', '搜索算法名称或标识...')}
        filters={[
          {
            columnId: 'taskType',
            title: t('thType', '任务形态'),
            options: taskTypeOptions,
          },
          {
            columnId: 'status',
            title: t('thStatus', '运行状态'),
            options: statusOptions,
          },
        ]}
        columnLabels={{
          name: t('thAlgorithm', '算法名称与标识'),
          taskType: t('thType', '任务形态'),
          platform: t('thPlatform', '目标平台'),
          resources: t('thResources', '资源消耗'),
          status: t('thStatus', '运行状态'),
          enable: t('thEnable', '调度开关'),
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
                    <Skeleton className='h-4 w-32' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-5 w-20 rounded-full' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-4 w-28' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-8 w-24' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-6 w-16 rounded-full' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-5 w-8 rounded-full' />
                  </TableCell>
                  <TableCell className='sticky inset-e-0 z-20 w-28 bg-card text-end'>
                    <Skeleton className='ms-auto size-8 rounded-md' />
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
                    <AlertCircle className='size-8' />
                    <span className='font-medium'>
                      {t('fetchFailed', '获取算法包列表失败')}
                    </span>
                    <Button
                      variant='outline'
                      size='sm'
                      onClick={onRetry}
                      className='mt-2'
                    >
                      <RefreshCw className='me-1 size-4' />
                      {t('retry', '重试')}
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
              // 空数据态（区分全空 vs 筛选无结果）
              <TableRow>
                <TableCell
                  colSpan={columns.length}
                  className='h-64 text-center'
                >
                  {isFiltered ? (
                    <div className='flex flex-col items-center justify-center gap-2'>
                      <p className='text-sm text-muted-foreground'>
                        {t('noMatchingAlgorithms', '没有找到符合条件的算法包')}
                      </p>
                      <Button
                        variant='ghost'
                        size='sm'
                        onClick={() => {
                          table.resetColumnFilters()
                          table.resetGlobalFilter()
                        }}
                      >
                        {t('clearFilter', '清空筛选条件')}
                      </Button>
                    </div>
                  ) : (
                    <div className='flex flex-col items-center justify-center gap-3 p-8'>
                      <div className='rounded-full bg-primary/10 p-4 text-primary'>
                        <Layers className='size-8' />
                      </div>
                      <div className='space-y-1'>
                        <h3 className='text-base font-semibold text-foreground'>
                          {t('emptyAlgorithmsTitle', '尚未安装任何算法包')}
                        </h3>
                        <p className='max-w-sm text-xs text-muted-foreground'>
                          {t(
                            'emptyAlgorithmsDesc',
                            '支持目标检测、图像分类、人脸与结构化算法包。上传符合规范的 .tar.gz 或 .zip 归档即可快速启用。'
                          )}
                        </p>
                      </div>
                      <Button
                        className='mt-2 gap-2'
                        size='sm'
                        onClick={handleOpenUpload}
                      >
                        <UploadCloud className='size-4' />
                        {t('uploadFirstAlgorithm', '安装第一个算法包')}
                      </Button>
                    </div>
                  )}
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
