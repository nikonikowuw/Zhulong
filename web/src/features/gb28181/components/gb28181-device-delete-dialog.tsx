import { AlertTriangle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { type Gb28181Device } from '../data/schema'

interface Gb28181DeviceDeleteDialogProps {
  open: boolean
  device: Gb28181Device | null
  isPending: boolean
  onOpenChange: (open: boolean) => void
  onConfirm: () => void
}

export function Gb28181DeviceDeleteDialog({
  open,
  device,
  isPending,
  onOpenChange,
  onConfirm,
}: Gb28181DeviceDeleteDialogProps) {
  const { t } = useTranslation('gb28181')

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={(next) => {
        if (!isPending && !next) onOpenChange(false)
      }}
      handleConfirm={onConfirm}
      isLoading={isPending}
      destructive
      title={
        <span className='flex items-center gap-1.5 text-destructive'>
          <AlertTriangle className='size-5 shrink-0' />
          {t('devices.deleteTitle')}
        </span>
      }
      desc={
        <div className='space-y-2 pt-1'>
          <p className='text-sm font-medium text-foreground'>
            {t('devices.deleteDesc')}
          </p>
          <div className='rounded-md bg-muted p-2.5 font-mono text-xs text-muted-foreground'>
            {device?.deviceId ?? ''}
          </div>
        </div>
      }
      confirmText={t('devices.deleteConfirm')}
      cancelBtnText={t('actions.cancel')}
    />
  )
}
