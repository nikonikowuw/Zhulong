import { type Row } from '@tanstack/react-table'
import {
  Image as ImageIcon,
  MoreHorizontal,
  Pencil,
  Trash2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { type Person } from '../data/schema'
import { usePersonsContext } from './persons-provider'

interface DataTableRowActionsProps {
  row: Row<Person>
}

export function PersonsDataTableRowActions({ row }: DataTableRowActionsProps) {
  const { t } = useTranslation('persons')
  const { setOpen, setCurrentRow } = usePersonsContext()
  const person = row.original

  const handleAction = (action: 'edit' | 'faces' | 'delete') => {
    setCurrentRow(person)
    setOpen(action)
  }

  return (
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger asChild>
        <Button
          variant='ghost'
          className='flex h-8 w-8 p-0 data-[state=open]:bg-muted'
          aria-label={t('table.columns.actions')}
        >
          <MoreHorizontal className='h-4 w-4' />
          <span className='sr-only'>{t('table.columns.actions')}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end' className='w-40'>
        <DropdownMenuItem onClick={() => handleAction('edit')}>
          <Pencil className='me-2 h-4 w-4' />
          {t('actions.edit')}
        </DropdownMenuItem>

        <DropdownMenuItem onClick={() => handleAction('faces')}>
          <ImageIcon className='me-2 h-4 w-4 text-primary' />
          {t('actions.manageFaces')}
        </DropdownMenuItem>

        <DropdownMenuSeparator />

        <DropdownMenuItem
          className='text-destructive focus:text-destructive'
          onClick={() => handleAction('delete')}
        >
          <Trash2 className='me-2 h-4 w-4' />
          {t('actions.delete')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
