import { memo } from 'react'
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
import { usePersonsActions } from './persons-provider'

interface PersonsRowActionsProps {
  /**
   * 目标人员。收 `Person` 而不是 `Row<Person>`：表格列与画廊卡片都要用，
   * 而画廊里根本没有一个完整的 row 对象 —— 曾经为了复用去伪造
   * `{ original: person } as Row<Person>`，等于把类型系统关掉换一次调用。
   */
  person: Person
}

export const PersonsDataTableRowActions = memo(
  function PersonsDataTableRowActions({ person }: PersonsRowActionsProps) {
    const { t } = useTranslation('persons')
    const { setOpen, setCurrentRow } = usePersonsActions()

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
)
