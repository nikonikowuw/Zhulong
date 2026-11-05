import { Globe, Radio } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import type { AlertDestination } from '../types'
import { DestinationForm } from './form/destination-form'

export interface AlertDestinationDialogProps {
  open: boolean
  destination?: AlertDestination | null
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
}

export function AlertDestinationDialog({
  open,
  destination,
  onOpenChange,
  onSuccess,
}: AlertDestinationDialogProps) {
  const { t } = useTranslation('alertPush')
  const isEdit = Boolean(destination)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='max-h-[90vh] overflow-y-auto sm:max-w-2xl'>
        <DialogHeader className='text-start'>
          <div className='flex items-center gap-3'>
            <div className='flex size-10 items-center justify-center rounded-xl border border-border/80 bg-muted/50 text-primary shadow-2xs'>
              {destination?.channelType === 'mqtt' ? (
                <Radio className='size-5' />
              ) : (
                <Globe className='size-5' />
              )}
            </div>
            <div className='space-y-0.5'>
              <DialogTitle className='text-base font-semibold tracking-tight'>
                {isEdit ? t('dialog.editTitle') : t('dialog.createTitle')}
              </DialogTitle>
              <DialogDescription className='text-xs text-muted-foreground'>
                {isEdit ? t('dialog.editDesc') : t('dialog.createDesc')}
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>

        {open ? (
          <DestinationForm
            destination={destination}
            onCancel={() => onOpenChange(false)}
            onSuccess={() => {
              onSuccess()
              onOpenChange(false)
            }}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  )
}
