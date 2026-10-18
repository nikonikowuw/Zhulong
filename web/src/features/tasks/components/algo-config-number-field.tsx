import { useTranslation } from 'react-i18next'
import { FormControl } from '@/components/ui/form'
import { NumberInput } from '@/components/ui/number-input'
import { Slider } from '@/components/ui/slider'
import {
  isNumericProperty,
  numericStep,
  type SchemaProperty,
} from '../data/algo-config-schema'

interface AlgoConfigNumberFieldProps {
  prop: SchemaProperty
  value: number | undefined
  label: string
  disabled: boolean
  onValueCommit: (next: number | undefined) => void
}

/**
 * 数值字段：有 `minimum` + `maximum` 时渲染 Slider + 数字框联动，
 * 否则渲染独立数字框（浮点允许任意精度，不再退化为整数步长）。
 */
export function AlgoConfigNumberField({
  prop,
  value,
  label,
  disabled,
  onValueCommit,
}: AlgoConfigNumberFieldProps) {
  const { t } = useTranslation('tasks')
  const isInteger = prop.type === 'integer'
  const min = prop.minimum
  const max = prop.maximum
  const step = numericStep(prop)

  if (!isNumericProperty(prop) || min === undefined || max === undefined) {
    return (
      <FormControl>
        <NumberInput
          value={value}
          integer={isInteger}
          disabled={disabled}
          aria-label={label}
          onValueCommit={onValueCommit}
          className='h-7 w-24 bg-background'
        />
      </FormControl>
    )
  }

  const sliderStep = step === 'any' ? 0.01 : step
  const sliderValue = value ?? min

  return (
    <div className='flex flex-1 items-center justify-end gap-2.5'>
      <Slider
        aria-label={t('dynamicForm.sliderAria', { name: label })}
        disabled={disabled}
        min={min}
        max={max}
        step={sliderStep}
        value={[sliderValue]}
        onValueChange={([next]) =>
          onValueCommit(isInteger ? Math.round(next) : next)
        }
        className='w-full min-w-28 sm:max-w-40 md:max-w-50'
      />
      <FormControl>
        <NumberInput
          value={value}
          integer={isInteger}
          min={min}
          max={max}
          disabled={disabled}
          aria-label={label}
          onValueCommit={(next) => onValueCommit(next ?? min)}
        />
      </FormControl>
      <span className='w-14 shrink-0 text-end font-mono text-[10px] text-muted-foreground/70'>
        [{min}-{max}]
      </span>
    </div>
  )
}
