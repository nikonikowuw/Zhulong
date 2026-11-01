import { type ColumnDef } from '@tanstack/react-table'
import { cn } from '@/lib/utils'
import { type AuditLog } from '../data/schema'
import {
  ActionCell,
  ActionNameCell,
  AuditColumnHeader,
  FormattedTimeCell,
  StatusBadge,
} from './AuditColumnsCells'

export const auditColumns: ColumnDef<AuditLog>[] = [
  {
    accessorKey: 'id',
    header: ({ column }) => (
      <AuditColumnHeader column={column} titleKey='table.columns.id' />
    ),
    cell: ({ row }) => (
      <span className='font-mono text-xs text-muted-foreground'>
        #{row.original.id}
      </span>
    ),
    enableSorting: false,
  },
  {
    accessorKey: 'createdAt',
    header: ({ column }) => (
      <AuditColumnHeader column={column} titleKey='table.columns.createdAt' />
    ),
    cell: ({ row }) => <FormattedTimeCell isoTime={row.original.createdAt} />,
    enableSorting: false,
  },
  {
    accessorKey: 'username',
    header: ({ column }) => (
      <AuditColumnHeader column={column} titleKey='table.columns.username' />
    ),
    cell: ({ row }) => (
      <span className='text-xs font-medium text-foreground'>
        {row.original.username || '-'}
      </span>
    ),
    enableSorting: false,
  },
  {
    accessorKey: 'action',
    header: ({ column }) => (
      <AuditColumnHeader column={column} titleKey='table.columns.action' />
    ),
    cell: ({ row }) => <ActionNameCell action={row.original.action} />,
    enableSorting: false,
  },
  {
    accessorKey: 'target',
    header: ({ column }) => (
      <AuditColumnHeader column={column} titleKey='table.columns.target' />
    ),
    cell: ({ row }) => (
      <span className='font-mono text-xs text-muted-foreground'>
        {row.original.target || '-'}
      </span>
    ),
    enableSorting: false,
  },
  {
    accessorKey: 'ip',
    header: ({ column }) => (
      <AuditColumnHeader column={column} titleKey='table.columns.ip' />
    ),
    cell: ({ row }) => (
      <span className='font-mono text-xs text-muted-foreground'>
        {row.original.ip || '-'}
      </span>
    ),
    enableSorting: false,
  },
  {
    accessorKey: 'status',
    header: ({ column }) => (
      <AuditColumnHeader column={column} titleKey='table.columns.status' />
    ),
    cell: ({ row }) => <StatusBadge status={row.original.status} />,
    enableSorting: false,
  },
  {
    id: 'actions',
    header: () => null,
    cell: ({ row }) => <ActionCell log={row.original} />,
    meta: {
      className: cn(
        'sticky inset-e-0 z-10 w-12 bg-card text-end',
        'before:pointer-events-none before:absolute before:inset-0 before:bg-muted/50 before:opacity-0 before:transition-opacity',
        'group-hover:before:opacity-100 group-data-[state=selected]:before:bg-muted group-data-[state=selected]:before:opacity-100'
      ),
    },
    enableSorting: false,
    enableHiding: false,
  },
]
