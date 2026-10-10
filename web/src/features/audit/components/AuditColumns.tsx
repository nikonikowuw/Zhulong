import { type ColumnDef } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import { DataTableColumnHeader } from '@/components/data-table'
import { type AuditLog } from '../data/schema'
import {
  ActionCell,
  ActionNameCell,
  FormattedTimeCell,
  StatusBadge,
} from './AuditColumnsCells'

export const auditColumns: ColumnDef<AuditLog>[] = [
  {
    accessorKey: 'id',
    header: ({ column }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const { t } = useTranslation('audit')
      return (
        <DataTableColumnHeader column={column} title={t('table.columns.id')} />
      )
    },
    cell: ({ row }) => (
      <span className='font-mono text-xs text-muted-foreground'>
        #{row.original.id}
      </span>
    ),
    enableSorting: false,
  },
  {
    accessorKey: 'createdAt',
    header: ({ column }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const { t } = useTranslation('audit')
      return (
        <DataTableColumnHeader
          column={column}
          title={t('table.columns.createdAt')}
        />
      )
    },
    cell: ({ row }) => <FormattedTimeCell isoTime={row.original.createdAt} />,
    enableSorting: false,
  },
  {
    accessorKey: 'username',
    header: ({ column }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const { t } = useTranslation('audit')
      return (
        <DataTableColumnHeader
          column={column}
          title={t('table.columns.username')}
        />
      )
    },
    cell: ({ row }) => (
      <span className='text-xs font-medium text-foreground'>
        {row.original.username || '-'}
      </span>
    ),
    enableSorting: false,
  },
  {
    accessorKey: 'action',
    header: ({ column }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const { t } = useTranslation('audit')
      return (
        <DataTableColumnHeader
          column={column}
          title={t('table.columns.action')}
        />
      )
    },
    cell: ({ row }) => <ActionNameCell action={row.original.action} />,
    enableSorting: false,
  },
  {
    accessorKey: 'target',
    header: ({ column }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const { t } = useTranslation('audit')
      return (
        <DataTableColumnHeader
          column={column}
          title={t('table.columns.target')}
        />
      )
    },
    cell: ({ row }) => (
      <span className='font-mono text-xs text-muted-foreground'>
        {row.original.target || '-'}
      </span>
    ),
    enableSorting: false,
  },
  {
    accessorKey: 'ip',
    header: ({ column }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const { t } = useTranslation('audit')
      return (
        <DataTableColumnHeader column={column} title={t('table.columns.ip')} />
      )
    },
    cell: ({ row }) => (
      <span className='font-mono text-xs text-muted-foreground'>
        {row.original.ip || '-'}
      </span>
    ),
    enableSorting: false,
  },
  {
    accessorKey: 'status',
    header: ({ column }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const { t } = useTranslation('audit')
      return (
        <DataTableColumnHeader
          column={column}
          title={t('table.columns.status')}
        />
      )
    },
    cell: ({ row }) => <StatusBadge status={row.original.status} />,
    enableSorting: false,
  },
  {
    id: 'actions',
    header: () => null,
    cell: ({ row }) => <ActionCell log={row.original} />,
    enableSorting: false,
  },
]
