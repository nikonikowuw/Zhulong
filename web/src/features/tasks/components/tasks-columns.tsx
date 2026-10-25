import { type ColumnDef } from '@tanstack/react-table'
import { cn } from '@/lib/utils'
import { type TaskRow } from '../data/task-row'
import {
  TaskActionsHeader,
  TaskAlgorithmCell,
  TaskCameraCell,
  TaskColumnHeader,
  TaskFpsCell,
  TaskIdsCell,
  TaskPointsCell,
  TaskStatusCell,
  TaskUpdatedAtCell,
} from './tasks-columns-cells'
import { TasksDataTableRowActions } from './tasks-data-table-row-actions'

/** 摄像头 / 算法 / 状态筛选都是多选（Faceted Filter），因此按数组包含匹配。 */
const includesFilter = (
  row: { getValue: (id: string) => unknown },
  id: string,
  value: string[]
) => {
  const cellValue = row.getValue(id)
  return Array.isArray(value) && value.includes(String(cellValue))
}

export const tasksColumns: ColumnDef<TaskRow>[] = [
  {
    id: 'taskId',
    accessorFn: (row) => row.taskId,
    header: ({ column }) => (
      <TaskColumnHeader column={column} titleKey='table.columns.taskId' />
    ),
    cell: ({ row }) => <TaskIdsCell row={row} />,
    enableSorting: false,
    meta: {
      className: cn(
        'inset-s-0 z-10 w-[180px] rounded-ss-[inherit] max-md:sticky'
      ),
    },
  },
  {
    id: 'camera',
    accessorFn: (row) => row.cameraId,
    header: ({ column }) => (
      <TaskColumnHeader column={column} titleKey='table.columns.camera' />
    ),
    cell: ({ row }) => <TaskCameraCell row={row} />,
    filterFn: (row, id, value) => includesFilter(row, id, value as string[]),
  },
  {
    id: 'algorithm',
    accessorFn: (row) => row.algorithmId,
    header: ({ column }) => (
      <TaskColumnHeader column={column} titleKey='table.columns.algorithm' />
    ),
    cell: ({ row }) => <TaskAlgorithmCell row={row} />,
    filterFn: (row, id, value) => includesFilter(row, id, value as string[]),
  },
  {
    id: 'sampleFps',
    accessorFn: (row) => row.sampleFps,
    header: ({ column }) => (
      <TaskColumnHeader column={column} titleKey='table.columns.sampleFps' />
    ),
    cell: ({ row }) => <TaskFpsCell row={row} />,
    meta: { className: 'w-[110px]' },
  },
  {
    id: 'computePoints',
    accessorFn: (row) => row.computePoints,
    header: ({ column }) => (
      <TaskColumnHeader
        column={column}
        titleKey='table.columns.computePoints'
      />
    ),
    cell: ({ row }) => <TaskPointsCell row={row} />,
    meta: { className: 'w-[110px]' },
  },
  {
    id: 'status',
    accessorFn: (row) => row.status,
    header: ({ column }) => (
      <TaskColumnHeader column={column} titleKey='table.columns.status' />
    ),
    cell: ({ row }) => <TaskStatusCell row={row} />,
    filterFn: (row, id, value) => includesFilter(row, id, value as string[]),
    meta: { className: 'w-[190px]' },
  },
  {
    id: 'updatedAt',
    accessorFn: (row) => row.updatedAt,
    header: ({ column }) => (
      <TaskColumnHeader column={column} titleKey='table.columns.updatedAt' />
    ),
    cell: ({ row }) => <TaskUpdatedAtCell row={row} />,
  },
  {
    id: 'actions',
    header: () => <TaskActionsHeader />,
    cell: ({ row }) => <TasksDataTableRowActions row={row} />,
    meta: {
      className: cn(
        'sticky inset-e-0 z-20 w-12 text-end',
        'bg-card',
        'before:pointer-events-none before:absolute before:inset-0 before:bg-muted/50 before:opacity-0 before:transition-opacity',
        'group-hover:before:opacity-100 group-data-[state=selected]:before:bg-muted group-data-[state=selected]:before:opacity-100'
      ),
    },
    enableSorting: false,
    enableHiding: false,
  },
]
