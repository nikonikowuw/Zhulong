import { useState } from 'react'
import { type Table } from '@tanstack/react-table'
import { Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { DataTableBulkActions as BulkActionsToolbar } from '@/components/data-table'
import { type Person } from '../data/schema'
import { PersonsMultiDeleteDialog } from './persons-multi-delete-dialog'

interface PersonsDataTableBulkActionsProps {
  table: Table<Person>
}

export function PersonsDataTableBulkActions({
  table,
}: PersonsDataTableBulkActionsProps) {
  const { t } = useTranslation('persons')
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false)

  const selectedRows = table.getFilteredSelectedRowModel().rows
  if (selectedRows.length === 0) return null

  return (
    <>
      <BulkActionsToolbar table={table} entityName='person'>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant='destructive'
              size='icon'
              className='size-8'
              onClick={() => setShowDeleteConfirm(true)}
              aria-label={t('actions.batchDelete')}
              title={t('actions.batchDelete')}
            >
              <Trash2 className='size-4' />
              <span className='sr-only'>{t('actions.batchDelete')}</span>
            </Button>
          </TooltipTrigger>
          <TooltipContent>
            <p>{t('actions.batchDelete')}</p>
          </TooltipContent>
        </Tooltip>
      </BulkActionsToolbar>

      <PersonsMultiDeleteDialog
        open={showDeleteConfirm}
        onOpenChange={setShowDeleteConfirm}
        table={table}
      />
    </>
  )
}
