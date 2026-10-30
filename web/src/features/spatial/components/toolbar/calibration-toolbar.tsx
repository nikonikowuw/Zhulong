import { Settings2, Save, Undo2, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'

interface CalibrationToolbarProps {
  isCalibrating: boolean
  hasUnsavedChanges: boolean
  isSaving: boolean
  onToggleCalibrating: () => void
  onSave: () => void
  onReset: () => void
  disabled?: boolean
}

export function CalibrationToolbar({
  isCalibrating,
  hasUnsavedChanges,
  isSaving,
  onToggleCalibrating,
  onSave,
  onReset,
  disabled,
}: CalibrationToolbarProps) {
  const { t } = useTranslation('spatial')

  return (
    <div className='flex items-center gap-2'>
      {!isCalibrating ? (
        <Button
          variant='outline'
          size='sm'
          className='h-9 gap-1.5'
          onClick={onToggleCalibrating}
          disabled={disabled}
        >
          <Settings2 className='h-4 w-4' />
          {t('calibration.enter')}
        </Button>
      ) : (
        <div className='flex items-center gap-2 rounded-lg border bg-background/95 p-1 shadow-sm backdrop-blur-sm'>
          <Button
            size='sm'
            className='h-8 gap-1.5'
            onClick={onSave}
            disabled={!hasUnsavedChanges || isSaving}
          >
            {isSaving ? (
              <Loader2 className='h-3.5 w-3.5 animate-spin' />
            ) : (
              <Save className='h-3.5 w-3.5' />
            )}
            {t('calibration.save')}
          </Button>

          {hasUnsavedChanges && (
            <Button
              variant='ghost'
              size='sm'
              className='h-8 gap-1 text-muted-foreground'
              onClick={onReset}
              disabled={isSaving}
            >
              <Undo2 className='h-3.5 w-3.5' />
              {t('calibration.reset')}
            </Button>
          )}

          <Button
            variant='secondary'
            size='sm'
            className='h-8'
            onClick={onToggleCalibrating}
            disabled={isSaving}
          >
            {t('calibration.exit')}
          </Button>
        </div>
      )}
    </div>
  )
}
