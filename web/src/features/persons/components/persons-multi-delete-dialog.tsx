import { useState } from 'react'
import { type Table } from '@tanstack/react-table'
import { AlertTriangle, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { type Person } from '../data/schema'
import { useDeletePerson } from '../hooks/use-persons'

type PersonsMultiDeleteDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  table: Table<Person>
}

const CONFIRM_WORD = 'DELETE'

export function PersonsMultiDeleteDialog({
  open,
  onOpenChange,
  table,
}: PersonsMultiDeleteDialogProps) {
  const { t } = useTranslation('persons')
  const [value, setValue] = useState('')
  const [isDeleting, setIsDeleting] = useState(false)
  const deleteMutation = useDeletePerson({ suppressGlobalToast: true })

  const selectedRows = table.getFilteredSelectedRowModel().rows
  const selectedPersons = selectedRows.map((row) => row.original)

  const handleDelete = async () => {
    if (value.trim() !== CONFIRM_WORD) {
      toast.error(t('validation.nameRequired'))
      return
    }

    setIsDeleting(true)
    try {
      await Promise.all(
        selectedPersons.map((p) => deleteMutation.mutateAsync(p.id))
      )
      toast.success(
        t('notifications.batchDeleteSuccess', { count: selectedPersons.length })
      )
      setValue('')
      table.resetRowSelection()
      onOpenChange(false)
    } catch {
      toast.error(t('notifications.batchDeleteError'))
    } finally {
      setIsDeleting(false)
    }
  }

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={(val) => {
        if (!isDeleting) {
          setValue('')
          onOpenChange(val)
        }
      }}
      form='persons-multi-delete-form'
      disabled={value.trim() !== CONFIRM_WORD || isDeleting}
      isLoading={isDeleting}
      destructive
      title={
        <span className='text-destructive'>
          <AlertTriangle
            className='me-1 inline-block stroke-destructive'
            size={18}
          />{' '}
          {t('dialogs.batchDeleteTitle', { count: selectedPersons.length })}
        </span>
      }
      desc={
        <form
          id='persons-multi-delete-form'
          onSubmit={(e) => {
            e.preventDefault()
            void handleDelete()
          }}
          className='space-y-4'
        >
          <p className='text-sm text-muted-foreground'>
            {t('dialogs.batchDeleteDesc', { count: selectedPersons.length })}
          </p>

          <Label className='my-4 flex flex-col items-start gap-1.5'>
            <span className='text-sm font-medium'>
              {t('dialogs.confirmDeletePrompt', { word: CONFIRM_WORD })}
            </span>
            <Input
              value={value}
              onChange={(e) => setValue(e.target.value)}
              placeholder={CONFIRM_WORD}
              disabled={isDeleting}
              autoFocus
            />
          </Label>
        </form>
      }
      confirmText={
        isDeleting ? (
          <span className='flex items-center gap-1.5'>
            <Loader2 className='size-4 animate-spin' />
            {t('actions.deleting')}
          </span>
        ) : (
          t('actions.batchDelete')
        )
      }
    />
  )
}
