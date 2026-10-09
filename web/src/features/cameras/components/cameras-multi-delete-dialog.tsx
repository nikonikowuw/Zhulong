'use client'

import { useState } from 'react'
import { type Table } from '@tanstack/react-table'
import { AlertTriangle, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { type Camera } from '../data/schema'
import { useDeleteCamera } from '../hooks/use-cameras'

type CamerasMultiDeleteDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  table: Table<Camera>
}

const CONFIRM_WORD = 'DELETE'

export function CamerasMultiDeleteDialog({
  open,
  onOpenChange,
  table,
}: CamerasMultiDeleteDialogProps) {
  const { t } = useTranslation('cameras')
  const [value, setValue] = useState('')
  const [isDeleting, setIsDeleting] = useState(false)
  const deleteMutation = useDeleteCamera()

  const selectedRows = table.getFilteredSelectedRowModel().rows
  const selectedCameras = selectedRows.map((row) => row.original)

  const handleDelete = async () => {
    if (value.trim() !== CONFIRM_WORD) {
      toast.error(t('dialog.typeToDeleteError', { word: CONFIRM_WORD }))
      return
    }

    setIsDeleting(true)
    try {
      await Promise.all(
        selectedCameras.map((cam) => deleteMutation.mutateAsync(cam.id))
      )
      toast.success(t('toast.batchDeleteSuccess'))
      setValue('')
      table.resetRowSelection()
      onOpenChange(false)
    } catch {
      toast.error('Batch delete failed')
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
      form='cameras-multi-delete-form'
      disabled={value.trim() !== CONFIRM_WORD || isDeleting}
      isLoading={isDeleting}
      destructive
      title={
        <span className='text-destructive'>
          <AlertTriangle
            className='me-1 inline-block stroke-destructive'
            size={18}
          />{' '}
          {t('dialog.batchDeleteTitle', { count: selectedCameras.length })}
        </span>
      }
      desc={
        <form
          id='cameras-multi-delete-form'
          onSubmit={(e) => {
            e.preventDefault()
            void handleDelete()
          }}
          className='space-y-4'
        >
          <p className='text-sm text-muted-foreground'>
            {t('dialog.batchDeleteDesc', { count: selectedCameras.length })}
          </p>

          <Label className='my-4 flex flex-col items-start gap-1.5'>
            <span className='text-sm font-medium'>
              {t('dialog.confirmDeletePrompt', { word: CONFIRM_WORD })}
            </span>
            <Input
              value={value}
              onChange={(e) => setValue(e.target.value)}
              placeholder={t('dialog.confirmDeletePlaceholder', {
                word: CONFIRM_WORD,
              })}
              disabled={isDeleting}
              autoFocus
            />
          </Label>

          <Alert variant='destructive'>
            <AlertTitle>{t('dialog.warningTitle')}</AlertTitle>
            <AlertDescription>{t('dialog.warningDesc')}</AlertDescription>
          </Alert>
        </form>
      }
      confirmText={
        isDeleting ? (
          <span className='flex items-center gap-1.5'>
            <Loader2 className='size-4 animate-spin' />
            {t('table.batchDelete')}
          </span>
        ) : (
          t('table.batchDelete')
        )
      }
    />
  )
}
