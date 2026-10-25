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
import { AlertCircle, LayoutGrid, List, RefreshCw, Video } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
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
import { ViewModeSwitch } from '@/components/view-mode-switch'
import { type Algorithm } from '@/features/algorithms/data/schema'
import { type Camera } from '@/features/cameras/data/schema'
import { type Task } from '../data/schema'
import { toTaskRows, type TaskRow } from '../data/task-row'
import { useChannelTasks } from '../hooks/use-channel-tasks'
import { useUpdateTask } from '../hooks/use-tasks'
import { ChannelTasksView } from './channel-tasks-view'
import { tasksColumns as columns } from './tasks-columns'
import { useTasksContext } from './tasks-provider'

const viewModeOptions = [
  {
    value: 'channel',
    label: 'views.channel',
    fallback: '通道视图',
    icon: LayoutGrid,
  },
  { value: 'table', label: 'views.table', fallback: '明细视图', icon: List },
] as const

interface TasksTableProps {
  tasks: Task[]
  cameras: Camera[]
  algorithms: Algorithm[]
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  search: Record<string, unknown>
  navigate: NavigateFn
}

export function TasksTable({
  tasks,
  cameras,
  algorithms,
  isLoading,
  isError,
  onRetry,
  search,
  navigate,
}: TasksTableProps) {
  const { t } = useTranslation('tasks')
  const { setOpen, setCurrentRow, setTargetCameraId } = useTasksContext()

  const [sorting, setSorting] = useState<SortingState>([])
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>({})

  // 双视角：通道聚合卡片 （业务主视角） ⇋ 明细表格
  const viewMode: 'channel' | 'table' =
    search.view === 'table' ? 'table' : 'channel'

  const handleViewModeChange = (mode: 'channel' | 'table') => {
    navigate({
      search: (prev) => ({
        ...prev,
        view: mode === 'channel' ? undefined : mode,
      }),
    })
  }

  const rows = useMemo(
    () => toTaskRows(tasks, cameras, algorithms),
    [tasks, cameras, algorithms]
  )
  const allChannelGroups = useChannelTasks(cameras, algorithms, tasks)

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
      { columnId: 'camera', searchKey: 'camera', type: 'array' },
      { columnId: 'algorithm', searchKey: 'algorithm', type: 'array' },
      { columnId: 'status', searchKey: 'status', type: 'array' },
    ],
  })

  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable<TaskRow>({
    data: rows,
    columns,
    state: {
      sorting,
      columnVisibility,
      columnFilters,
      pagination,
      globalFilter,
    },
    enableRowSelection: false,
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
    getRowId: (row) => row.id,
  })

  const pageCount = table.getPageCount()
  useEffect(() => {
    ensurePageInRange(pageCount)
  }, [pageCount, ensurePageInRange])

  const cameraOptions = cameras.map((camera) => ({
    label: camera.name,
    value: camera.id,
  }))
  const algorithmOptions = algorithms.map((algorithm) => ({
    label: algorithm.name,
    value: algorithm.id,
  }))
  const statusOptions = [
    { label: t('table.statusRunning', '运行中'), value: 'running' },
    { label: t('table.statusPaused', '已暂停'), value: 'paused' },
  ]

  const isFiltered = Boolean(globalFilter) || columnFilters.length > 0

  /**
   * 通道卡片视图的筛选语义：**筛卡片，不裁剪卡内算法列表**。
   * 卡片是通道级聚合视图，卡内始终展示该通道的全部订阅项；
   * 算法 / 状态筛选命中组内任意一项即保留整张卡片。
   */
  const channelGroups = useMemo(() => {
    const filterValues = (columnId: string) =>
      (columnFilters.find((f) => f.id === columnId)?.value as
        | string[]
        | undefined) ?? []

    const cameraFilter = filterValues('camera')
    const algorithmFilter = filterValues('algorithm')
    const statusFilter = filterValues('status')
    const keyword = (globalFilter ?? '').trim().toLowerCase()

    return allChannelGroups.filter((group) => {
      if (cameraFilter.length > 0 && !cameraFilter.includes(group.camera.id)) {
        return false
      }

      if (
        algorithmFilter.length > 0 &&
        !group.items.some((item) =>
          algorithmFilter.includes(item.algorithmItem.algorithmId)
        )
      ) {
        return false
      }

      if (
        statusFilter.length > 0 &&
        !group.items.some((item) =>
          statusFilter.includes(
            item.task.enabled && item.algorithmItem.enabled
              ? 'running'
              : 'paused'
          )
        )
      ) {
        return false
      }

      if (!keyword) return true

      const haystack = [
        group.camera.name,
        group.camera.id,
        group.task?.id ?? '',
        ...group.items.map((item) => item.algorithm?.name ?? ''),
        ...group.items.map((item) => item.algorithmItem.algorithmId),
      ]
        .join(' ')
        .toLowerCase()

      return haystack.includes(keyword)
    })
  }, [allChannelGroups, columnFilters, globalFilter])

  const updateMutation = useUpdateTask()

  const handleOpenChannelConfig = (cameraId?: string) => {
    setCurrentRow(null)
    setTargetCameraId(cameraId ?? null)
    setOpen('channelTask')
  }

  const handleToggleChannelTask = (task: Task) => {
    updateMutation.mutate(
      { id: task.id, data: { enabled: !task.enabled } },
      {
        onSuccess: () => {
          toast.success(
            !task.enabled
              ? t('table.channelResumed', '通道分析任务已恢复运行')
              : t('table.channelPaused', '通道分析任务已暂停')
          )
        },
        onError: () => {
          toast.error(t('table.toggleError', '更新任务状态失败'))
        },
      }
    )
  }

  const handleDeleteChannelTask = (task: Task) => {
    setCurrentRow(task)
    setOpen('delete')
  }

  return (
    <div className='flex flex-1 flex-col gap-4'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div className='flex-1'>
          <DataTableToolbar
            table={table}
            searchPlaceholder={t(
              'searchPlaceholder',
              '搜索任务 ID、摄像头或算法名称...'
            )}
            filters={[
              {
                columnId: 'camera',
                title: t('table.columns.camera', '关联摄像头'),
                options: cameraOptions,
              },
              {
                columnId: 'algorithm',
                title: t('table.columns.algorithm', '绑定算法包'),
                options: algorithmOptions,
              },
              {
                columnId: 'status',
                title: t('table.columns.status', '运行状态'),
                options: statusOptions,
              },
            ]}
            columnLabels={{
              taskId: t('table.columns.taskId', '任务 / 订阅 ID'),
              camera: t('table.columns.camera', '关联摄像头'),
              algorithm: t('table.columns.algorithm', '绑定算法包'),
              sampleFps: t('table.columns.sampleFps', '采样帧率'),
              computePoints: t('table.columns.computePoints', '算力开销'),
              status: t('table.columns.status', '运行状态'),
              updatedAt: t('table.columns.updatedAt', '更新时间'),
            }}
          />
        </div>

        <ViewModeSwitch
          value={viewMode}
          onChange={handleViewModeChange}
          options={viewModeOptions.map((option) => ({
            ...option,
            label: t(option.label, option.fallback),
          }))}
        />
      </div>

      {viewMode === 'channel' ? (
        <ChannelTasksView
          groups={channelGroups}
          onConfigureCamera={handleOpenChannelConfig}
          onToggleChannelTask={handleToggleChannelTask}
          onDeleteChannelTask={handleDeleteChannelTask}
        />
      ) : (
        <>
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
                  Array.from({ length: 5 }).map((_, index) => (
                    <TableRow key={`skeleton-${index}`}>
                      <TableCell>
                        <Skeleton className='h-4 w-28' />
                      </TableCell>
                      <TableCell>
                        <Skeleton className='h-4 w-24' />
                      </TableCell>
                      <TableCell>
                        <Skeleton className='h-4 w-24' />
                      </TableCell>
                      <TableCell>
                        <Skeleton className='h-4 w-12' />
                      </TableCell>
                      <TableCell>
                        <Skeleton className='h-4 w-12' />
                      </TableCell>
                      <TableCell>
                        <Skeleton className='h-6 w-32 rounded-full' />
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
                  <TableRow>
                    <TableCell
                      colSpan={columns.length}
                      className='h-48 text-center'
                    >
                      <div className='flex flex-col items-center justify-center gap-2 text-destructive'>
                        <AlertCircle className='h-8 w-8' />
                        <span className='font-medium'>
                          {t('table.loadFailed', '加载分析任务失败')}
                        </span>
                        <Button
                          variant='outline'
                          size='sm'
                          onClick={onRetry}
                          className='mt-2'
                        >
                          <RefreshCw className='me-1 h-4 w-4' />
                          {t('actions.retry', '重试')}
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ) : table.getRowModel().rows?.length ? (
                  table.getRowModel().rows.map((row) => (
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
                      colSpan={columns.length}
                      className='h-64 text-center'
                    >
                      <div className='flex flex-col items-center justify-center gap-2'>
                        <div className='flex h-12 w-12 items-center justify-center rounded-full bg-muted'>
                          <Video className='h-6 w-6 text-muted-foreground' />
                        </div>
                        <span className='text-base font-semibold text-foreground'>
                          {isFiltered
                            ? t('table.filterEmpty', '未找到匹配的分析任务')
                            : t('table.empty', '暂无分析任务记录')}
                        </span>
                        <p className='max-w-sm text-sm text-muted-foreground'>
                          {isFiltered
                            ? t(
                                'table.filterEmptyDesc',
                                '请调整搜索关键词或筛选条件后重试。'
                              )
                            : t(
                                'table.emptyDesc',
                                '尚未向任何摄像头分配分析算法，点击「配置通道任务」开始。'
                              )}
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
                            {t('table.resetFilter', '重置筛选')}
                          </Button>
                        ) : (
                          <Button
                            size='sm'
                            onClick={() => handleOpenChannelConfig()}
                            className='mt-2'
                          >
                            {t('newChannelTask', '配置通道任务')}
                          </Button>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </div>

          {/* 无数据时不渲染分页（否则会显示 “Page 1 of 0”） */}
          {table.getFilteredRowModel().rows.length > 0 && (
            <DataTablePagination table={table} />
          )}
        </>
      )}
    </div>
  )
}
