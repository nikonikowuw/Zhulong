import { AlertTriangle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { useDeleteCamera } from '../hooks/use-cameras'
import { useCamerasContext } from './cameras-provider'

export function CamerasDeleteDialog() {
  const { t } = useTranslation('cameras')
  const { open, setOpen, currentRow } = useCamerasContext()
  const deleteMutation = useDeleteCamera()

  const isOpen = open === 'delete' && Boolean(currentRow)

  const handleDelete = async () => {
    if (!currentRow?.id) return
    try {
      await deleteMutation.mutateAsync(currentRow.id)
      toast.success(t('toast.deleteSuccess'))
      setOpen(null)
    } catch {
      toast.error('Failed to delete camera')
    }
  }

  return (
    <ConfirmDialog
      open={isOpen}
      onOpenChange={(val) => !deleteMutation.isPending && !val && setOpen(null)}
      handleConfirm={handleDelete}
      isLoading={deleteMutation.isPending}
      destructive
      title={
        <span className='flex items-center gap-1.5 text-destructive'>
          <AlertTriangle className='h-5 w-5 shrink-0' />
          {t('dialog.deleteTitle')}
        </span>
      }
      desc={
        <div className='space-y-2 pt-1'>
          <p className='text-sm font-medium text-foreground'>
            {t('dialog.deleteDesc', { name: currentRow?.name || '' })}
          </p>
          <div className='rounded-md bg-muted p-2.5 font-mono text-xs text-muted-foreground'>
            ID: {currentRow?.id}
          </div>
        </div>
      }
      confirmText={t('actions.delete')}
      cancelBtnText='取消'
    />
  )
}
