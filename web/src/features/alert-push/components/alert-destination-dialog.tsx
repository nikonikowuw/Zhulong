import { Globe, Radio } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
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
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side='right'
        className='flex h-full w-full flex-col gap-0 p-0 sm:max-w-xl md:max-w-2xl'
      >
        <SheetHeader className='shrink-0 border-b px-6 py-4 pe-14 text-start'>
          <div className='flex items-center gap-3'>
            <div className='flex size-10 shrink-0 items-center justify-center rounded-xl border border-border/80 bg-muted/50 text-primary shadow-2xs'>
              {destination?.channelType === 'mqtt' ? (
                <Radio className='size-5' />
              ) : (
                <Globe className='size-5' />
              )}
            </div>
            <div className='min-w-0 space-y-0.5'>
              <SheetTitle className='text-base font-semibold tracking-tight'>
                {isEdit ? t('dialog.editTitle') : t('dialog.createTitle')}
              </SheetTitle>
              <SheetDescription className='text-xs text-muted-foreground'>
                {isEdit ? t('dialog.editDesc') : t('dialog.createDesc')}
              </SheetDescription>
            </div>
          </div>
        </SheetHeader>

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
      </SheetContent>
    </Sheet>
  )
}

// 语义别名：抽屉化后新代码可直接按 Drawer 命名引用，原导出名继续兼容既有调用点
export const AlertDestinationDrawer = AlertDestinationDialog
