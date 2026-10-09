import { type Row } from '@tanstack/react-table'
import { Activity, MoreHorizontal, Pencil, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { type Camera } from '../data/schema'
import { useCamerasContext } from './cameras-provider'

interface DataTableRowActionsProps {
  row: Row<Camera>
}

export function CamerasDataTableRowActions({ row }: DataTableRowActionsProps) {
  const { t } = useTranslation('cameras')
  const { setOpen, setCurrentRow } = useCamerasContext()
  const camera = row.original

  const handleAction = (action: 'edit' | 'diagnose' | 'delete') => {
    setCurrentRow(camera)
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

        <DropdownMenuItem onClick={() => handleAction('diagnose')}>
          <Activity className='me-2 h-4 w-4 text-primary' />
          {t('actions.diagnose')}
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
