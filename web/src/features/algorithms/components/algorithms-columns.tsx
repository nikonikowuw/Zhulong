import { type ColumnDef } from '@tanstack/react-table'
import { cn } from '@/lib/utils'
import { Checkbox } from '@/components/ui/checkbox'
import { type Algorithm, type AlgorithmStatus } from '../data/schema'
import {
  AlgorithmColumnHeader,
  AlgorithmNameCell,
  AlgorithmPlatformCell,
  AlgorithmResourcesCell,
  AlgorithmStatusCell,
  AlgorithmSwitchCell,
  AlgorithmTaskTypeCell,
} from './algorithms-columns-cells'
import { AlgorithmsDataTableRowActions } from './algorithms-data-table-row-actions'

export const algorithmsColumns: ColumnDef<Algorithm>[] = [
  {
    id: 'select',
    header: ({ table }) => (
      <Checkbox
        checked={
          table.getIsAllPageRowsSelected() ||
          (table.getIsSomePageRowsSelected() && 'indeterminate')
        }
        onCheckedChange={(value) => table.toggleAllPageRowsSelected(!!value)}
        aria-label='Select all'
        className='translate-y-0.5'
      />
    ),
    meta: {
      className: cn('inset-s-0 z-10 rounded-ss-[inherit] max-md:sticky'),
    },
    cell: ({ row }) => (
      <Checkbox
        checked={row.getIsSelected()}
        onCheckedChange={(value) => row.toggleSelected(!!value)}
        aria-label='Select row'
        className='translate-y-0.5'
      />
    ),
    enableSorting: false,
    enableHiding: false,
  },
  {
    accessorKey: 'name',
    id: 'name',
    header: ({ column }) => (
      <AlgorithmColumnHeader
        column={column}
        titleKey='thAlgorithm'
        defaultTitle='算法名称与标识'
      />
    ),
    cell: ({ row }) => <AlgorithmNameCell algo={row.original} />,
    enableHiding: false,
  },
  {
    accessorKey: 'taskType',
    id: 'taskType',
    header: ({ column }) => (
      <AlgorithmColumnHeader
        column={column}
        titleKey='thType'
        defaultTitle='任务形态'
      />
    ),
    cell: ({ row }) => (
      <AlgorithmTaskTypeCell taskType={row.original.taskType} />
    ),
    filterFn: (row, id, filterValues: string[]) => {
      if (!filterValues || filterValues.length === 0) return true
      return filterValues.includes(row.getValue(id))
    },
  },
  {
    id: 'platform',
    header: ({ column }) => (
      <AlgorithmColumnHeader
        column={column}
        titleKey='thPlatform'
        defaultTitle='目标平台'
      />
    ),
    cell: ({ row }) => <AlgorithmPlatformCell algo={row.original} />,
  },
  {
    id: 'resources',
    header: ({ column }) => (
      <AlgorithmColumnHeader
        column={column}
        titleKey='thResources'
        defaultTitle='资源消耗'
      />
    ),
    cell: ({ row }) => <AlgorithmResourcesCell algo={row.original} />,
  },
  {
    accessorKey: 'status',
    id: 'status',
    header: ({ column }) => (
      <AlgorithmColumnHeader
        column={column}
        titleKey='thStatus'
        defaultTitle='运行状态'
      />
    ),
    cell: ({ row }) => (
      <AlgorithmStatusCell status={row.original.status as AlgorithmStatus} />
    ),
    filterFn: (row, id, filterValues: string[]) => {
      if (!filterValues || filterValues.length === 0) return true
      return filterValues.includes(row.getValue(id))
    },
  },
  {
    id: 'enable',
    header: ({ column }) => (
      <AlgorithmColumnHeader
        column={column}
        titleKey='thEnable'
        defaultTitle='调度开关'
      />
    ),
    cell: ({ row }) => <AlgorithmSwitchCell algo={row.original} />,
  },
  {
    id: 'actions',
    meta: {
      className: cn(
        'sticky inset-e-0 z-10 w-28 text-end',
        'relative bg-card',
        'before:pointer-events-none before:absolute before:inset-0 before:bg-muted/50 before:opacity-0 before:transition-opacity',
        'group-hover:before:opacity-100 group-data-[state=selected]:before:bg-muted group-data-[state=selected]:before:opacity-100'
      ),
    },
    cell: ({ row }) => <AlgorithmsDataTableRowActions row={row} />,
    enableSorting: false,
    enableHiding: false,
  },
]
