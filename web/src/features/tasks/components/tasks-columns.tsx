import { type ColumnDef } from '@tanstack/react-table'
import { type ChannelTaskGroup, type Task } from '../data/schema'
import {
  ChannelActionsCell,
  ChannelAlgorithmsCell,
  ChannelCameraCell,
  ChannelExpanderCell,
  ChannelPointsCell,
  ChannelStatusCell,
  ChannelUpdatedAtCell,
  TaskActionsHeader,
  TaskColumnHeader,
} from './tasks-columns-cells'

export interface CreateTasksColumnsOptions {
  onConfigureCamera?: (cameraId: string) => void
  onDeleteChannelTask?: (task: Task) => void
}

export function createTasksColumns(
  options: CreateTasksColumnsOptions = {}
): ColumnDef<ChannelTaskGroup>[] {
  return [
    {
      id: 'expander',
      header: () => null,
      cell: ({ row }) => <ChannelExpanderCell row={row} />,
      enableSorting: false,
      enableHiding: false,
      meta: { className: 'w-10 px-2' },
    },
    {
      id: 'camera',
      accessorFn: (row) => row.camera.name,
      header: ({ column }) => (
        <TaskColumnHeader
          column={column}
          titleKey='table.columns.camera'
          titleFallback='关联摄像头'
        />
      ),
      cell: ({ row }) => <ChannelCameraCell row={row} />,
      enableSorting: true,
      meta: { className: 'min-w-[240px]' },
    },
    {
      id: 'status',
      accessorFn: (row) => (row.task?.enabled ? 'running' : 'paused'),
      header: ({ column }) => (
        <TaskColumnHeader
          column={column}
          titleKey='table.columns.status'
          titleFallback='通道状态'
        />
      ),
      cell: ({ row }) => <ChannelStatusCell row={row} />,
      meta: { className: 'w-32' },
    },
    {
      id: 'algorithm',
      accessorFn: (row) => row.items.length,
      header: ({ column }) => (
        <TaskColumnHeader
          column={column}
          titleKey='table.columns.algorithm'
          titleFallback='已配算法'
        />
      ),
      cell: ({ row }) => <ChannelAlgorithmsCell row={row} />,
      meta: { className: 'w-28' },
    },
    {
      id: 'computePoints',
      accessorFn: (row) => row.totalChannelPoints,
      header: ({ column }) => (
        <TaskColumnHeader
          column={column}
          titleKey='table.columns.computePoints'
          titleFallback='通道总算力'
        />
      ),
      cell: ({ row }) => <ChannelPointsCell row={row} />,
      meta: { className: 'w-28' },
    },
    {
      id: 'updatedAt',
      accessorFn: (row) => row.task?.updatedAt ?? '',
      header: ({ column }) => (
        <TaskColumnHeader
          column={column}
          titleKey='table.columns.updatedAt'
          titleFallback='更新时间'
        />
      ),
      cell: ({ row }) => <ChannelUpdatedAtCell row={row} />,
      meta: { className: 'w-36' },
    },
    {
      id: 'actions',
      header: () => <TaskActionsHeader />,
      cell: ({ row }) => (
        <ChannelActionsCell
          row={row}
          onConfigureCamera={options.onConfigureCamera}
          onDeleteChannelTask={options.onDeleteChannelTask}
        />
      ),
      enableSorting: false,
      enableHiding: false,
      meta: { className: 'w-36 text-end' },
    },
  ]
}
