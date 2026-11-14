import { Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  type Gb28181Device,
  type UpsertGb28181DeviceRequest,
} from '../data/schema'
import { Gb28181DeviceFormFields } from './gb28181-device-form-fields'

/** footer 的提交按钮在表单外，靠 `form` 属性关联。 */
const FORM_ID = 'gb28181-device-form'

interface Gb28181DeviceDialogProps {
  open: boolean
  device: Gb28181Device | null
  isPending: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (
    deviceId: string,
    payload: UpsertGb28181DeviceRequest
  ) => Promise<void>
}

/**
 * 预置设备 / 轮换 Digest 密码。
 *
 * 密码永不回显：编辑态留空即表示沿用已存密文，
 * 这样改名字不会顺手把凭据抹掉，也不需要把密钥重新输入一遍。
 */
export function Gb28181DeviceDialog({
  open,
  device,
  isPending,
  onOpenChange,
  onSubmit,
}: Gb28181DeviceDialogProps) {
  const { t } = useTranslation('gb28181')
  const isEdit = device !== null

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!isPending) onOpenChange(next)
      }}
    >
      <DialogContent className='w-full sm:max-w-lg'>
        <DialogHeader className='text-start'>
          <DialogTitle>
            {isEdit ? t('devices.editTitle') : t('devices.addTitle')}
          </DialogTitle>
          <DialogDescription>{t('devices.dialogDesc')}</DialogDescription>
        </DialogHeader>

        {/* 关闭时 Radix 卸载内容，配合 key 确保切换设备时表单状态完全重置。 */}
        <Gb28181DeviceFormFields
          key={device?.deviceId ?? 'create'}
          device={device}
          isPending={isPending}
          formId={FORM_ID}
          onSubmit={onSubmit}
          onClose={() => onOpenChange(false)}
        />

        <DialogFooter className='gap-y-2 pt-2 sm:items-center sm:justify-end'>
          <DialogClose asChild>
            <Button variant='outline' disabled={isPending}>
              {t('actions.cancel')}
            </Button>
          </DialogClose>
          <Button
            type='submit'
            form={FORM_ID}
            disabled={isPending}
            className='min-w-24'
          >
            {isPending && <Loader2 className='me-1.5 size-4 animate-spin' />}
            {isPending ? t('actions.saving') : t('actions.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
