import { Minus, Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { NumberInput } from '@/components/ui/number-input'

interface FpsStepperProps {
  value: number
  min: number
  max: number
  disabled?: boolean
  onChange: (next: number) => void
}

/** 采样帧率步进器：−/+ 按钮与数字框共用同一钳制区间。 */
export function FpsStepper({
  value,
  min,
  max,
  disabled = false,
  onChange,
}: FpsStepperProps) {
  const { t } = useTranslation('tasks')

  return (
    <div className='flex items-center overflow-hidden rounded-md border border-input bg-background shadow-xs focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/50'>
      <Button
        type='button'
        variant='ghost'
        size='icon'
        disabled={disabled || value <= min}
        aria-label={t('dialog.fpsDecrease', '降低采样帧率')}
        onClick={() => onChange(Math.max(min, value - 1))}
        className='h-6 w-5 rounded-none border-e border-border text-muted-foreground'
      >
        <Minus className='h-3 w-3' />
      </Button>
      <NumberInput
        value={value}
        integer
        min={min}
        max={max}
        disabled={disabled}
        aria-label={t('dialog.fpsLabel', '采样帧率')}
        onValueCommit={(next) => onChange(next ?? min)}
        className='h-6 w-12 border-0 bg-transparent px-0 shadow-none focus-visible:ring-0 dark:bg-transparent'
      />
      <Button
        type='button'
        variant='ghost'
        size='icon'
        disabled={disabled || value >= max}
        aria-label={t('dialog.fpsIncrease', '提高采样帧率')}
        onClick={() => onChange(Math.min(max, value + 1))}
        className='h-6 w-5 rounded-none border-s border-border text-muted-foreground'
      >
        <Plus className='h-3 w-3' />
      </Button>
    </div>
  )
}
