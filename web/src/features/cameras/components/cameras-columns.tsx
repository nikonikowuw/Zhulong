import { type ColumnDef } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Checkbox } from '@/components/ui/checkbox'
import { DataTableColumnHeader } from '@/components/data-table'
import { LongText } from '@/components/long-text'
import { type Camera } from '../data/schema'
import {
  CameraRtspCell,
  CameraStatusBadge,
  CameraSwitchCell,
  StreamSpecCell,
} from './cameras-columns-cells'
import { CamerasDataTableRowActions } from './cameras-data-table-row-actions'

export const camerasColumns: ColumnDef<Camera>[] = [
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
      className: cn('inset-s-0 z-10 rounded-tl-[inherit] max-md:sticky'),
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
    accessorKey: 'id',
    header: ({ column }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const { t } = useTranslation('cameras')
      return (
        <DataTableColumnHeader column={column} title={t('table.columns.id')} />
      )
    },
    cell: ({ row }) => {
      return (
        <span className='font-mono text-xs text-muted-foreground'>
          {row.original.id}
        </span>
      )
    },
  },
  {
    accessorKey: 'name',
    header: ({ column }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const { t } = useTranslation('cameras')
      return (
        <DataTableColumnHeader
          column={column}
          title={t('table.columns.name')}
        />
      )
    },
    cell: ({ row }) => {
      const camera = row.original
      return (
        <div className='flex items-center ps-2'>
          <LongText className='max-w-48 font-medium text-foreground'>
            {camera.name}
          </LongText>
        </div>
      )
    },
    meta: {
      className: cn('ps-0.5 max-md:sticky @4xl/content:table-cell'),
    },
    enableHiding: false,
  },
  {
    id: 'rtspUrl',
    header: ({ column }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const { t } = useTranslation('cameras')
      return (
        <DataTableColumnHeader
          column={column}
          title={t('table.columns.rtspUrl')}
        />
      )
    },
    cell: ({ row }) => <CameraRtspCell camera={row.original} />,
  },
  {
    accessorKey: 'health',
    header: ({ column }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const { t } = useTranslation('cameras')
      return (
        <DataTableColumnHeader
          column={column}
          title={t('table.columns.status')}
        />
      )
    },
    cell: ({ row }) => <CameraStatusBadge camera={row.original} />,
  },
  {
    id: 'mainStream',
    header: ({ column }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const { t } = useTranslation('cameras')
      return (
        <DataTableColumnHeader
          column={column}
          title={t('table.columns.mainStream')}
        />
      )
    },
    cell: ({ row }) => {
      const stream = row.original.streams?.find((s) => s.role === 'main')
      return <StreamSpecCell stream={stream} />
    },
  },
  {
    id: 'subStream',
    header: ({ column }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const { t } = useTranslation('cameras')
      return (
        <DataTableColumnHeader
          column={column}
          title={t('table.columns.subStream')}
        />
      )
    },
    cell: ({ row }) => {
      const stream = row.original.streams?.find((s) => s.role === 'sub')
      return <StreamSpecCell stream={stream} />
    },
  },
  {
    accessorKey: 'enabled',
    header: ({ column }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const { t } = useTranslation('cameras')
      return (
        <DataTableColumnHeader
          column={column}
          title={t('table.columns.enabled')}
        />
      )
    },
    cell: ({ row }) => <CameraSwitchCell camera={row.original} />,
  },
  {
    accessorKey: 'updatedAt',
    header: ({ column }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const { t } = useTranslation('cameras')
      return (
        <DataTableColumnHeader
          column={column}
          title={t('table.columns.updatedAt')}
        />
      )
    },
    cell: ({ row }) => {
      const raw = row.getValue('updatedAt') as string
      if (!raw) return <span className='text-xs text-muted-foreground'>-</span>
      const date = new Date(raw)
      return (
        <span className='text-xs whitespace-nowrap text-muted-foreground'>
          {date.toLocaleString()}
        </span>
      )
    },
  },
  {
    id: 'actions',
    header: () => <span className='sr-only'>Actions</span>,
    cell: ({ row }) => <CamerasDataTableRowActions row={row} />,
    meta: {
      className: cn(
        'sticky end-0 right-0 z-20 w-12 text-end',
        'bg-card',
        'before:pointer-events-none before:absolute before:inset-0 before:bg-muted/50 before:opacity-0 before:transition-opacity',
        'group-hover:before:opacity-100 group-data-[state=selected]:before:bg-muted group-data-[state=selected]:before:opacity-100'
      ),
    },
    enableSorting: false,
    enableHiding: false,
  },
]
