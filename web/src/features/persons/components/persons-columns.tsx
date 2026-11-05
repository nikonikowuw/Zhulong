import { type ColumnDef } from '@tanstack/react-table'
import { cn } from '@/lib/utils'
import { Checkbox } from '@/components/ui/checkbox'
import { type Person } from '../data/schema'
import {
  PersonAvatarCell,
  PersonColumnHeader,
  PersonNameCell,
  PersonSourceCell,
  PersonsFacesCell,
} from './persons-columns-cells'
import { PersonsDataTableRowActions } from './persons-data-table-row-actions'

export const personsColumns: ColumnDef<Person>[] = [
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
      className: cn(
        'inset-s-0 z-10 w-10 rounded-tl-[inherit] text-center max-md:sticky'
      ),
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
    id: 'avatar',
    header: ({ column }) => (
      <PersonColumnHeader column={column} titleKey='table.columns.avatar' />
    ),
    cell: ({ row }) => <PersonAvatarCell person={row.original} />,
    enableSorting: false,
    meta: {
      className: 'w-16 text-center',
    },
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
