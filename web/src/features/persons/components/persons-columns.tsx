import { type ColumnDef } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { DataTableColumnHeader } from '@/components/data-table'
import { type Person } from '../data/schema'
import { PersonsFacesCell } from './persons-columns-cells'
import { PersonsDataTableRowActions } from './persons-data-table-row-actions'

export const personsColumns: ColumnDef<Person>[] = [
  {
    accessorKey: 'name',
    header: ({ column }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const { t } = useTranslation('persons')
      return (
        <DataTableColumnHeader
          column={column}
          title={t('table.columns.name')}
        />
      )
    },
    cell: ({ row }) => {
      return (
        <div className='flex flex-col'>
          <span className='font-medium text-foreground'>{row.original.name}</span>
          <span className='font-mono text-[11px] text-muted-foreground'>
            {row.original.id}
          </span>
        </div>
      )
    },
    enableSorting: false,
  },
  {
    accessorKey: 'personId',
    header: ({ column }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const { t } = useTranslation('persons')
      return (
        <DataTableColumnHeader
          column={column}
          title={t('table.columns.personId')}
        />
      )
    },
    cell: ({ row }) => {
      return (
        <span className='font-mono text-sm text-foreground'>
          {row.original.personId || '-'}
        </span>
      )
    },
    enableSorting: false,
  },
  {
    id: 'faces',
    header: ({ column }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const { t } = useTranslation('persons')
      return (
        <DataTableColumnHeader
          column={column}
          title={t('table.columns.faceCount')}
        />
      )
    },
    cell: ({ row }) => <PersonsFacesCell person={row.original} />,
    enableSorting: false,
  },
  {
    accessorKey: 'createdAt',
    header: ({ column }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const { t } = useTranslation('persons')
      return (
        <DataTableColumnHeader
          column={column}
          title={t('table.columns.createdAt')}
        />
      )
    },
    cell: ({ row }) => {
      const raw = row.getValue('createdAt') as string
      if (!raw) return <span className='text-xs text-muted-foreground'>-</span>
      const date = new Date(raw)
      return (
        <span className='text-xs whitespace-nowrap text-muted-foreground'>
          {date.toLocaleString()}
        </span>
      )
    },
    enableSorting: false,
  },
  {
    id: 'actions',
    header: () => <span className='sr-only'>Actions</span>,
    cell: ({ row }) => <PersonsDataTableRowActions row={row} />,
    meta: {
      className: cn(
        'sticky end-0 right-0 z-20 w-12 text-end',
        'bg-card',
        'transition-colors duration-200'
      ),
    },
  },
]
