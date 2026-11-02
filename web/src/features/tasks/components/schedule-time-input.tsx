import React, { useState } from 'react'

export interface ScheduleTimeInputProps {
  value: string
  onChange: (val: string) => void
  disabled?: boolean
}

/** 24小时制精细时间输入框 (无 spinner 截断，支持键盘任意打字与自动纠错) */
export function ScheduleTimeInput({
  value,
  onChange,
  disabled = false,
}: ScheduleTimeInputProps): React.JSX.Element {
  const [localVal, setLocalVal] = useState(value)
  const [prevValue, setPrevValue] = useState(value)

  if (value !== prevValue) {
    setPrevValue(value)
    setLocalVal(value)
  }

  const commit = () => {
    const trimmed = localVal.trim()
    const match = trimmed.match(/^(\d{1,2}):?(\d{0,2})$/)
    if (match) {
      let h = parseInt(match[1], 10)
      let m = parseInt(match[2] || '0', 10)
      if (isNaN(h)) h = 0
      if (isNaN(m)) m = 0
      h = Math.max(0, Math.min(24, h))
      m = Math.max(0, Math.min(59, m))
      if (h === 24) m = 0
      const formatted = `${h.toString().padStart(2, '0')}:${m.toString().padStart(2, '0')}`
      setLocalVal(formatted)
      onChange(formatted)
    } else {
      setLocalVal(value)
    }
  }

  return (
    <input
      type='text'
      disabled={disabled}
      value={localVal}
      onChange={(e) => setLocalVal(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === 'Enter') {
          commit()
          ;(e.target as HTMLInputElement).blur()
        }
      }}
      className='h-7 w-18 rounded-md border border-input bg-background px-1.5 text-center font-mono text-xs font-semibold text-foreground shadow-2xs transition-colors outline-none focus:border-ring focus:ring-1 focus:ring-ring disabled:opacity-50'
      placeholder='08:00'
    />
  )
}
