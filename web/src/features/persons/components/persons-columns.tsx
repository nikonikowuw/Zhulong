import { type ColumnDef } from '@tanstack/react-table'
import { formatLocalDateTime } from '@/lib/csv'
import { cn } from '@/lib/utils'
import { type Person } from '../data/schema'
import {
  PersonActionsHeader,
  PersonAvatarCell,
  PersonColumnHeader,
  PersonNameCell,
  PersonRowSelectCheckbox,
  PersonsFacesCell,
  PersonSelectAllCheckbox,
  PersonSourceCell,
} from './persons-columns-cells'
import { PersonsDataTableRowActions } from './persons-data-table-row-actions'

/**
 * 列定义。
 *
 * 所有需要翻译的地方都在 `header` / `cell` 渲染函数（组件）内部调用
 * `useTranslation`，没有任何 `t()` 在模块求值期执行 —— 否则切换语言后列头会
 * 继续显示旧语言，必须整页刷新才更新。
 */
export const personsColumns: ColumnDef<Person>[] = [
  {
    id: 'select',
    header: ({ table }) => <PersonSelectAllCheckbox table={table} />,
    meta: {
      // `bg-card` 不是装饰：`max-md` 断点下这一列是 sticky，没有不透明底色时
      // 横向滚动的内容会从列底下穿过去（规范 §2.2 点名过的 bug）。
      className: cn(
        'inset-s-0 z-10 w-10 rounded-ss-[inherit] bg-card text-center',
        'max-md:sticky'
      ),
    },
    cell: ({ row }) => <PersonRowSelectCheckbox row={row} />,
    enableSorting: false,
    enableHiding: false,
  },
  {
    id: 'avatar',
    header: ({ column }) => (
      <PersonColumnHeader column={column} titleKey='table.columns.avatar' />
    ),
    cell: ({ row }) => <PersonAvatarCell person={row.original} />,
    enableSorting: false,
    meta: { className: 'w-16 text-center' },
  },
  {
    accessorKey: 'name',
    header: ({ column }) => (
      <PersonColumnHeader column={column} titleKey='table.columns.name' />
    ),
    cell: ({ row }) => <PersonNameCell person={row.original} />,
    enableSorting: false,
  },
  {
    accessorKey: 'personId',
    header: ({ column }) => (
      <PersonColumnHeader column={column} titleKey='table.columns.personId' />
    ),
    cell: ({ row }) => {
      const pid = row.original.personId
      return (
        <span className='font-mono text-sm text-foreground'>
          {pid && pid.trim() ? pid : '-'}
        </span>
      )
    },
    enableSorting: false,
  },
  {
    accessorKey: 'source',
    header: ({ column }) => (
      <PersonColumnHeader column={column} titleKey='table.columns.source' />
    ),
    cell: ({ row }) => <PersonSourceCell person={row.original} />,
    enableSorting: false,
  },
  {
    id: 'faces',
    accessorFn: (row) =>
      row.faces && row.faces.length > 0 ? 'enrolled' : 'missing',
    header: ({ column }) => (
      <PersonColumnHeader column={column} titleKey='table.columns.faceCount' />
    ),
    cell: ({ row }) => <PersonsFacesCell person={row.original} />,
    enableSorting: false,
  },
  {
    accessorKey: 'createdAt',
    header: ({ column }) => (
      <PersonColumnHeader column={column} titleKey='table.columns.createdAt' />
    ),
    cell: ({ row }) => {
      const raw = row.getValue('createdAt') as string | undefined
      // 统一的 `yyyy-MM-dd HH:mm:ss` 本地时区格式，与导出 CSV 一致：
      // `toLocaleString()` 随语言重排字段次序，同一列跨行会左右跳。
      const formatted = raw ? formatLocalDateTime(raw) : ''
      return (
        <span className='text-xs whitespace-nowrap text-muted-foreground'>
          {formatted || '-'}
        </span>
      )
    },
    enableSorting: false,
  },
  {
    id: 'actions',
    header: () => <PersonActionsHeader />,
    cell: ({ row }) => <PersonsDataTableRowActions person={row.original} />,
    meta: {
      // 双层伪元素悬浮：底层实心 `bg-card` 遮挡滚动物，上层淡入高亮。
      // 严禁 `group-hover:bg-muted/50` —— 半透明底色会冲掉实色，导致穿透。
      className: cn(
        'sticky inset-e-0 z-20 w-12 bg-card text-end',
        'before:pointer-events-none before:absolute before:inset-0 before:bg-muted/50 before:opacity-0 before:transition-opacity',
        'group-hover:before:opacity-100 group-data-[state=selected]:before:bg-muted group-data-[state=selected]:before:opacity-100'
      ),
    },
    enableSorting: false,
    enableHiding: false,
  },
]
