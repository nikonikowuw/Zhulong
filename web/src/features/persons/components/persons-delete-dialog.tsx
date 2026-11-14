import { AlertTriangle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatLocalDateTime } from '@/lib/csv'
import { errorMessageOf } from '@/lib/handle-server-error'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { type Person } from '../data/schema'
import { useDeletePerson } from '../hooks/use-persons'
import { usePersonsActions, usePersonsState } from './persons-provider'

export function PersonsDeleteDialog() {
  const { open, currentRow } = usePersonsState()
  if (open !== 'delete' || !currentRow) return null

  return <PersonsDeleteDialogContent currentRow={currentRow} />
}

function PersonsDeleteDialogContent({ currentRow }: { currentRow: Person }) {
  const { t } = useTranslation('persons')
  const { setOpen } = usePersonsActions()
  const deleteMutation = useDeletePerson({ suppressGlobalToast: true })

  const handleDelete = async () => {
    if (!currentRow.id) return
    try {
      await deleteMutation.mutateAsync(currentRow.id)
      toast.success(t('notifications.deleteSuccess'))
      setOpen(null)
    } catch (err: unknown) {
      toast.error(errorMessageOf(err))
    }
  }

  return (
    <ConfirmDialog
      open
      onOpenChange={(val) => !deleteMutation.isPending && !val && setOpen(null)}
      handleConfirm={handleDelete}
      isLoading={deleteMutation.isPending}
      destructive
      title={
        <span className='flex items-center gap-1.5 text-destructive'>
          <AlertTriangle className='h-5 w-5 shrink-0' />
          {t('dialogs.deleteTitle')}
        </span>
      }
      desc={
        <div className='space-y-2 pt-1'>
          <p className='text-sm font-medium text-foreground'>
            {t('dialogs.deleteDesc', { name: currentRow.name || '' })}
          </p>
          <div className='rounded-md bg-muted p-2.5 font-mono text-xs text-muted-foreground'>
            ID: {currentRow.id} | {t('fields.personId')}:{' '}
            {currentRow.personId ?? '-'} | {t('fields.createdAt')}:{' '}
            {currentRow.createdAt
              ? formatLocalDateTime(currentRow.createdAt)
              : '-'}
          </div>
        </div>
      }
      confirmText={t('actions.delete')}
      cancelBtnText={t('actions.cancel')}
    />
  )
}
