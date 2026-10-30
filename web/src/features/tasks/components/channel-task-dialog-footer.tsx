import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { DialogFooter } from '@/components/ui/dialog'

export interface ChannelTaskDialogFooterProps {
  /** 预算超限或缺少摄像头时为 false。 */
  canSave: boolean
  isSaving: boolean
  onSave: () => void
  onClose: () => void
}

/** 底部操作条：取消与保存下发调度。 */
export function ChannelTaskDialogFooter({
  canSave,
  isSaving,
  onSave,
  onClose,
}: ChannelTaskDialogFooterProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  return (
    <DialogFooter className='flex items-center justify-end gap-2 border-t border-border bg-card px-6 py-3'>
      <Button
        type='button'
        variant='outline'
        size='sm'
        className='h-8 text-xs'
        onClick={onClose}
      >
        {t('dialog.cancel', '取消')}
      </Button>

      <Button
        type='button'
        size='sm'
        disabled={!canSave}
        className='h-8 gap-1 text-xs'
        onClick={onSave}
      >
        {isSaving
          ? t('dialog.saving', '下发同步中...')
          : t('dialog.save', '保存并下发调度')}
      </Button>
    </DialogFooter>
  )
}
