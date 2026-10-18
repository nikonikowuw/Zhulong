import * as React from 'react'
import { cn } from '@/lib/utils'
import { Input } from '@/components/ui/input'

interface NumberInputProps extends Omit<
  React.ComponentProps<'input'>,
  'value' | 'onChange' | 'type' | 'min' | 'max' | 'inputMode' | 'step'
> {
  /** 受控数值。`undefined` 表示“未配置”，会渲染为空文本。 */
  value: number | undefined
  /** 仅在失焦、回车或步进时提交；空文本提交 `undefined`。 */
  onValueCommit: (next: number | undefined) => void
  integer?: boolean
  min?: number
  max?: number
}

function formatNumber(value: number | undefined): string {
  return value === undefined || Number.isNaN(value) ? '' : String(value)
}

function parseNumber(raw: string, integer: boolean): number | undefined {
  const trimmed = raw.trim()
  if (trimmed === '') return undefined
  const parsed = integer
    ? Number.parseInt(trimmed, 10)
    : Number.parseFloat(trimmed)
  return Number.isNaN(parsed) ? undefined : parsed
}

function clampNumber(
  value: number,
  min: number | undefined,
  max: number | undefined
): number {
  let next = value
  if (min !== undefined) next = Math.max(min, next)
  if (max !== undefined) next = Math.min(max, next)
  return next
}

/**
 * 紧凑数值输入框。
 *
 * 与原生 `type="number"` 的区别：草稿文本由本地状态持有，只在提交时解析。
 * 这样 `0.` 这类浏览器的中间态不会被 value 净化算法清空，浮点小数可以正常录入。
 */
function NumberInput({
  value,
  onValueCommit,
  integer = false,
  min,
  max,
  className,
  onBlur,
  onKeyDown,
  ...props
}: NumberInputProps) {
  const [draft, setDraft] = React.useState(() => formatNumber(value))
  const [syncedValue, setSyncedValue] = React.useState(value)

  // 渲染期同步：外部值变化（父级钳制、重置、切换算法）时刷新草稿，无需 useEffect 往返。
  if (value !== syncedValue) {
    setSyncedValue(value)
    setDraft(formatNumber(value))
  }

  const commit = () => {
    const parsed = parseNumber(draft, integer)
    if (parsed === undefined) {
      setDraft(formatNumber(value))
      onValueCommit(undefined)
      return
    }
    const next = clampNumber(integer ? Math.round(parsed) : parsed, min, max)
    setDraft(formatNumber(next))
    onValueCommit(next)
  }

  return (
    <Input
      {...props}
      type='text'
      inputMode={integer ? 'numeric' : 'decimal'}
      autoComplete='off'
      value={draft}
      onChange={(event) => setDraft(event.target.value)}
      onBlur={(event) => {
        commit()
        onBlur?.(event)
      }}
      onKeyDown={(event) => {
        if (event.key === 'Enter') {
          event.preventDefault()
          commit()
          return
        }
        if (event.key === 'Escape') {
          setDraft(formatNumber(value))
          return
        }
        onKeyDown?.(event)
      }}
      className={cn(
        'h-6 w-14 px-1 text-center font-mono text-xs font-semibold tabular-nums',
        className
      )}
    />
  )
}

export { NumberInput }
export { clampNumber, formatNumber, parseNumber }
