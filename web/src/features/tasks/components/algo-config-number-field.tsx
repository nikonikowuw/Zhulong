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

function getStepPrecision(step: number | 'any'): number {
  if (step === 'any') return 2
  const str = String(step)
  const dot = str.indexOf('.')
  return dot === -1 ? 0 : str.length - dot - 1
}

function cleanRound(
  val: number,
  step: number | 'any',
  isInteger: boolean
): number {
  if (isInteger) return Math.round(val)
  const precision = Math.min(6, getStepPrecision(step))
  return Number(val.toFixed(precision))
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
          className='h-7 w-36 bg-background text-center font-mono text-xs'
        />
      </FormControl>
    )
  }

  const sliderStep = step === 'any' ? 0.01 : step
  const sliderValue = value ?? min

  return (
    <div className='flex flex-1 items-center justify-end gap-2'>
      <Slider
        aria-label={t('dynamicForm.sliderAria', { name: label })}
        disabled={disabled}
        min={min}
        max={max}
        step={sliderStep}
        value={[sliderValue]}
        onValueChange={([next]) =>
          onValueCommit(cleanRound(next, sliderStep, isInteger))
        }
        className='w-full min-w-24 sm:max-w-36 md:max-w-44'
      />
      <FormControl>
        <NumberInput
          value={value}
          integer={isInteger}
          min={min}
          max={max}
          disabled={disabled}
          aria-label={label}
          onValueCommit={(next) => {
            if (next === undefined) {
              onValueCommit(min)
              return
            }
            if (isInteger) {
              onValueCommit(Math.round(next))
              return
            }
            // 浮点数手动输入：保留至多 6 位有效精度，不按 slider 步长强行截断为整数
            const manualPrecision = Math.max(getStepPrecision(step), 4)
            onValueCommit(Number(next.toFixed(Math.min(6, manualPrecision))))
          }}
          className='h-7 w-16 shrink-0 bg-background text-center font-mono text-xs'
        />
      </FormControl>
    </div>
  )
}
