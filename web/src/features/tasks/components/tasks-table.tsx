import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  type ExpandedState,
  type SortingState,
  type VisibilityState,
  getCoreRowModel,
  getExpandedRowModel,
  getFacetedRowModel,
  getFacetedUniqueValues,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { type NavigateFn, useTableUrlState } from '@/hooks/use-table-url-state'
import { type Algorithm } from '@/features/algorithms/data/schema'
import { type Camera } from '@/features/cameras/data/schema'
import { type ChannelTaskGroup, type Task } from '../data/schema'
import { useChannelTasks } from '../hooks/use-channel-tasks'
import { useUpdateTask } from '../hooks/use-tasks'
import { ChannelTasksView } from './channel-tasks-view'
import { createTasksColumns } from './tasks-columns'
import { useTasksContext } from './tasks-provider'
import { TasksTableDetail } from './tasks-table-detail'
import { TasksTableToolbar } from './tasks-table-toolbar'

interface TasksTableProps {
  tasks: Task[]
  cameras: Camera[]
  algorithms: Algorithm[]
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  search: Record<string, unknown>
  navigate: NavigateFn
  defaultExpanded?: boolean
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
  defaultExpanded = false,
}: TasksTableProps) {
  const { t } = useTranslation('tasks')
  const { setOpen, setCurrentRow, setTargetCameraId, setTargetAlgorithmId } =
    useTasksContext()

  const [sorting, setSorting] = useState<SortingState>([])
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>({})
  // 默认折叠，保持列表清爽聚焦通道总览
  const [expanded, setExpanded] = useState<ExpandedState>(
    defaultExpanded ? true : {}
  )

  // 双视角：通道聚合卡片 （业务主视角） ⇋ 二级展开表格
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
   * 筛选语义：筛选命中通道（卡片和二级表格共享），不裁剪卡内算法
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

  // 二级表格主行仅展示已配置任务的通道
  const configuredGroups = useMemo(
    () => channelGroups.filter((group) => group.items.length > 0 && group.task),
    [channelGroups]
  )

  const updateMutation = useUpdateTask()

  const handleOpenChannelConfig = useCallback(
    (cameraId?: string) => {
      setCurrentRow(null)
      setTargetCameraId(cameraId ?? null)
      setTargetAlgorithmId(null)
      setOpen('channelTask')
    },
    [setCurrentRow, setTargetCameraId, setTargetAlgorithmId, setOpen]
  )

  const handleToggleChannelTask = useCallback(
    (task: Task) => {
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
    },
    [updateMutation, t]
  )

  const handleDeleteChannelTask = useCallback(
    (task: Task) => {
      setCurrentRow(task)
      setOpen('delete')
    },
    [setCurrentRow, setOpen]
  )

  const columns = useMemo(
    () =>
      createTasksColumns({
        onConfigureCamera: handleOpenChannelConfig,
        onDeleteChannelTask: handleDeleteChannelTask,
      }),
    [handleOpenChannelConfig, handleDeleteChannelTask]
  )

  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable<ChannelTaskGroup>({
    data: configuredGroups,
    columns,
    state: {
      sorting,
      columnVisibility,
      columnFilters,
      pagination,
      globalFilter,
      expanded,
    },
    enableRowSelection: false,
    onSortingChange: setSorting,
    onColumnFiltersChange,
    onColumnVisibilityChange: setColumnVisibility,
    onPaginationChange,
    onGlobalFilterChange,
    onExpandedChange: setExpanded,
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFacetedRowModel: getFacetedRowModel(),
    getFacetedUniqueValues: getFacetedUniqueValues(),
    getRowCanExpand: (row) => (row.original?.items?.length ?? 0) > 0,
    getExpandedRowModel: getExpandedRowModel(),
    getRowId: (row) => row.camera.id,
  })

  const pageCount = table.getPageCount()
  useEffect(() => {
    ensurePageInRange(pageCount)
  }, [pageCount, ensurePageInRange])

  return (
    <div className='flex flex-1 flex-col gap-4'>
      <TasksTableToolbar
        table={table}
        viewMode={viewMode}
        configuredCount={configuredGroups.length}
        cameraOptions={cameraOptions}
        algorithmOptions={algorithmOptions}
        statusOptions={statusOptions}
        onViewModeChange={handleViewModeChange}
      />

      {viewMode === 'channel' ? (
        <ChannelTasksView
          groups={channelGroups}
          onConfigureCamera={handleOpenChannelConfig}
          onToggleChannelTask={handleToggleChannelTask}
          onDeleteChannelTask={handleDeleteChannelTask}
        />
      ) : (
        <TasksTableDetail
          table={table}
          columnCount={columns.length}
          isLoading={isLoading}
          isError={isError}
          onRetry={onRetry}
          isFiltered={isFiltered}
          onOpenChannelConfig={handleOpenChannelConfig}
        />
      )}
    </div>
  )
}
