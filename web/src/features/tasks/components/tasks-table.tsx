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
import { type NavigateFn, useTableUrlState } from '@/hooks/use-table-url-state'
import { type Algorithm } from '@/features/algorithms/contract'
import { type Camera } from '@/features/cameras/contract'
import { type ChannelTaskGroup, type Task } from '../data/schema'
import { useChannelTasks } from '../hooks/use-channel-tasks'
import { createTasksColumns } from './tasks-columns'
import { useTasksActions } from './tasks-provider'
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
    useTasksActions()

  const [sorting, setSorting] = useState<SortingState>([])
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>({})
  // 默认折叠，保持列表清爽聚焦通道总览
  const [expanded, setExpanded] = useState<ExpandedState>(
    defaultExpanded ? true : {}
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

  const cameraOptions = useMemo(
    () =>
      cameras.map((camera) => ({
        label: camera.name,
        value: camera.id,
      })),
    [cameras]
  )
  const algorithmOptions = useMemo(
    () =>
      algorithms.map((algorithm) => ({
        label: algorithm.name,
        value: algorithm.id,
      })),
    [algorithms]
  )
  const statusOptions = useMemo(
    () => [
      { label: t('table.statusRunning', '运行中'), value: 'running' },
      { label: t('table.statusPaused', '已暂停'), value: 'paused' },
    ],
    [t]
  )

  const isFiltered = Boolean(globalFilter) || columnFilters.length > 0

  /**
   * 筛选语义：筛选命中通道（卡片和二级表格共享），不裁剪卡内算法
   */
  const channelGroups = useMemo(() => {
    const filterSet = (columnId: string) =>
      new Set(
        (columnFilters.find((f) => f.id === columnId)?.value as
          | string[]
          | undefined) ?? []
      )

    const cameraFilter = filterSet('camera')
    const algorithmFilter = filterSet('algorithm')
    const statusFilter = filterSet('status')
    const keyword = (globalFilter ?? '').trim().toLowerCase()

    return allChannelGroups.filter((group) => {
      if (cameraFilter.size > 0 && !cameraFilter.has(group.camera.id)) {
        return false
      }

      if (
        algorithmFilter.size > 0 &&
        !group.items.some((item) =>
          algorithmFilter.has(item.algorithmItem.algorithmId)
        )
      ) {
        return false
      }

      if (
        statusFilter.size > 0 &&
        !group.items.some((item) => {
          const isItemRunning =
            item.task.enabled &&
            item.algorithmItem.enabled &&
            group.camera.enabled &&
            group.camera.health !== 'offline' &&
            group.camera.health !== 'error' &&
            item.algorithm?.status !== 'DISABLED' &&
            item.algorithm?.status !== 'CRASHED' &&
            item.algorithm?.status !== 'CRASH_LOOP'
          return statusFilter.has(isItemRunning ? 'running' : 'paused')
        })
      ) {
        return false
      }

      if (!keyword) return true

      const parts = [group.camera.name, group.camera.id, group.task?.id ?? '']
      for (const item of group.items) {
        if (item.algorithm?.name) parts.push(item.algorithm.name)
        parts.push(item.algorithmItem.algorithmId)
      }

      return parts.join(' ').toLowerCase().includes(keyword)
    })
  }, [allChannelGroups, columnFilters, globalFilter])

  // 二级表格主行仅展示已配置任务的通道
  const configuredGroups = useMemo(
    () => channelGroups.filter((group) => group.items.length > 0 && group.task),
    [channelGroups]
  )

  const handleOpenChannelConfig = useCallback(
    (cameraId?: string) => {
      setCurrentRow(null)
      setTargetCameraId(cameraId ?? null)
      setTargetAlgorithmId(null)
      setOpen('channelTask')
    },
    [setCurrentRow, setTargetCameraId, setTargetAlgorithmId, setOpen]
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
        configuredCount={configuredGroups.length}
        cameraOptions={cameraOptions}
        algorithmOptions={algorithmOptions}
        statusOptions={statusOptions}
      />

      <TasksTableDetail
        table={table}
        columnCount={columns.length}
        isLoading={isLoading}
        isError={isError}
        onRetry={onRetry}
        isFiltered={isFiltered}
        onOpenChannelConfig={handleOpenChannelConfig}
      />
    </div>
  )
}
