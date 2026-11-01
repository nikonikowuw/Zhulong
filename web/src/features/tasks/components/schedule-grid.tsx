import { useCallback, useEffect, useMemo, useRef } from 'react'
import { CalendarClock, Check, RotateCcw, SunMedium, Moon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  DAY_KEYS,
  SLOTS_PER_DAY,
  intervalsToSlots,
  parseSchedule,
  serializeSchedule,
  slotsToIntervals,
  type DayKey,
  type WeekSchedule,
} from '../data/schedule-config'

export interface ScheduleGridProps {
  value?: string
  onChange: (scheduleJson: string) => void
  disabled?: boolean
}

const TIME_MARKS = [0, 4, 8, 12, 16, 20, 24] as const

export function ScheduleGrid({
  value,
  onChange,
  disabled = false,
}: ScheduleGridProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  const schedule = useMemo(() => parseSchedule(value), [value])
  const enabled = schedule.enabled

  // 7 days x 48 slots grid state
  const gridState = useMemo(() => {
    const map: Record<DayKey, boolean[]> = {
      monday: intervalsToSlots(schedule.days?.monday),
      tuesday: intervalsToSlots(schedule.days?.tuesday),
      wednesday: intervalsToSlots(schedule.days?.wednesday),
      thursday: intervalsToSlots(schedule.days?.thursday),
      friday: intervalsToSlots(schedule.days?.friday),
      saturday: intervalsToSlots(schedule.days?.saturday),
      sunday: intervalsToSlots(schedule.days?.sunday),
    }
    return map
  }, [schedule])

  // Dragging interaction state
  const isDraggingRef = useRef(false)
  const dragTargetValueRef = useRef(true)

  const commitGrid = useCallback(
    (newGrid: Record<DayKey, boolean[]>, isEnabled: boolean) => {
      if (!isEnabled) {
        onChange('{}')
        return
      }
      const newDays: Partial<
        Record<DayKey, ReturnType<typeof slotsToIntervals>>
      > = {}
      for (const day of DAY_KEYS) {
        const intervals = slotsToIntervals(newGrid[day])
        if (intervals.length > 0) {
          newDays[day] = intervals
        }
      }
      const newSchedule: WeekSchedule = {
        enabled: true,
        days: newDays,
      }
      onChange(serializeSchedule(newSchedule))
    },
    [onChange]
  )

  const handlePointerDown = (day: DayKey, slotIdx: number) => {
    if (disabled || !enabled) return
    isDraggingRef.current = true
    const currentVal = gridState[day][slotIdx]
    const nextVal = !currentVal
    dragTargetValueRef.current = nextVal

    const nextSlots = [...gridState[day]]
    nextSlots[slotIdx] = nextVal
    const nextGrid = { ...gridState, [day]: nextSlots }
    commitGrid(nextGrid, enabled)
  }

  const handlePointerEnter = (day: DayKey, slotIdx: number) => {
    if (!isDraggingRef.current || disabled || !enabled) return
    const currentVal = gridState[day][slotIdx]
    if (currentVal !== dragTargetValueRef.current) {
      const nextSlots = [...gridState[day]]
      nextSlots[slotIdx] = dragTargetValueRef.current
      const nextGrid = { ...gridState, [day]: nextSlots }
      commitGrid(nextGrid, enabled)
    }
  }

  useEffect(() => {
    const handlePointerUp = () => {
      isDraggingRef.current = false
    }
    window.addEventListener('pointerup', handlePointerUp)
    return () => {
      window.removeEventListener('pointerup', handlePointerUp)
    }
  }, [])

  const handleToggleEnabled = (checked: boolean) => {
    if (!checked) {
      onChange('{}')
    } else {
      const hasAnyActive = Object.values(gridState).some((slots) =>
        slots.some(Boolean)
      )
      if (!hasAnyActive) {
        handlePresetAll()
      } else {
        commitGrid(gridState, true)
      }
    }
  }

  const handleDayLabelClick = (day: DayKey) => {
    if (disabled || !enabled) return
    const allActive = gridState[day].every(Boolean)
    const nextSlots = new Array<boolean>(SLOTS_PER_DAY).fill(!allActive)
    const nextGrid = { ...gridState, [day]: nextSlots }
    commitGrid(nextGrid, true)
  }

  const handlePresetAll = () => {
    if (disabled) return
    const nextGrid: Record<DayKey, boolean[]> = {
      monday: new Array(SLOTS_PER_DAY).fill(true),
      tuesday: new Array(SLOTS_PER_DAY).fill(true),
      wednesday: new Array(SLOTS_PER_DAY).fill(true),
      thursday: new Array(SLOTS_PER_DAY).fill(true),
      friday: new Array(SLOTS_PER_DAY).fill(true),
      saturday: new Array(SLOTS_PER_DAY).fill(true),
      sunday: new Array(SLOTS_PER_DAY).fill(true),
    }
    commitGrid(nextGrid, true)
  }

  const handlePresetWorkdays = () => {
    if (disabled) return
    // Workdays 08:00 - 18:00 (slots 16..35)
    const workdaySlots = new Array<boolean>(SLOTS_PER_DAY).fill(false)
    for (let i = 16; i < 36; i++) workdaySlots[i] = true
    const emptySlots = new Array<boolean>(SLOTS_PER_DAY).fill(false)

    const nextGrid: Record<DayKey, boolean[]> = {
      monday: [...workdaySlots],
      tuesday: [...workdaySlots],
      wednesday: [...workdaySlots],
      thursday: [...workdaySlots],
      friday: [...workdaySlots],
      saturday: [...emptySlots],
      sunday: [...emptySlots],
    }
    commitGrid(nextGrid, true)
  }

  const handlePresetNights = () => {
    if (disabled) return
    // Nights 20:00 - 06:00 (slots 40..47 and 0..11)
    const nightSlots = new Array<boolean>(SLOTS_PER_DAY).fill(false)
    for (let i = 0; i < 12; i++) nightSlots[i] = true
    for (let i = 40; i < 48; i++) nightSlots[i] = true

    const nextGrid: Record<DayKey, boolean[]> = {
      monday: [...nightSlots],
      tuesday: [...nightSlots],
      wednesday: [...nightSlots],
      thursday: [...nightSlots],
      friday: [...nightSlots],
      saturday: [...nightSlots],
      sunday: [...nightSlots],
    }
    commitGrid(nextGrid, true)
  }

  const handlePresetClear = () => {
    if (disabled) return
    const emptyGrid: Record<DayKey, boolean[]> = {
      monday: new Array(SLOTS_PER_DAY).fill(false),
      tuesday: new Array(SLOTS_PER_DAY).fill(false),
      wednesday: new Array(SLOTS_PER_DAY).fill(false),
      thursday: new Array(SLOTS_PER_DAY).fill(false),
      friday: new Array(SLOTS_PER_DAY).fill(false),
      saturday: new Array(SLOTS_PER_DAY).fill(false),
      sunday: new Array(SLOTS_PER_DAY).fill(false),
    }
    commitGrid(emptyGrid, true)
  }

  const totalActiveSlots = useMemo(() => {
    if (!enabled) return 7 * SLOTS_PER_DAY
    let count = 0
    for (const day of DAY_KEYS) {
      count += gridState[day].filter(Boolean).length
    }
    return count
  }, [enabled, gridState])

  const totalHours = (totalActiveSlots * 0.5).toFixed(1)
  const coveragePercent = Math.round(
    (totalActiveSlots / (7 * SLOTS_PER_DAY)) * 100
  )

  return (
    <div className='flex flex-col gap-3 rounded-xl border border-border bg-card p-4 shadow-xs'>
      {/* 头部标题与总开关 */}
      <div className='flex items-center justify-between'>
        <div className='flex items-center gap-2'>
          <CalendarClock className='size-3.5 text-muted-foreground' />
          <Label className='text-xs font-semibold text-foreground'>
            {t('schedule.title', '分时布防计划')}
          </Label>
          {!enabled ? (
            <Badge
              variant='outline'
              className='h-5 text-[10px] text-muted-foreground'
            >
              {t('schedule.alwaysActive', '24×7 全天运行')}
            </Badge>
          ) : (
            <Badge variant='default' className='h-5 font-mono text-[10px]'>
              {coveragePercent}% 覆盖
            </Badge>
          )}
        </div>
        <div className='flex items-center gap-2'>
          <Label
            htmlFor='schedule-enabled-switch'
            className='cursor-pointer text-[11px] text-muted-foreground'
          >
            {t('schedule.enableSchedule', '启用分时计划')}
          </Label>
          <Switch
            id='schedule-enabled-switch'
            checked={enabled}
            disabled={disabled}
            onCheckedChange={handleToggleEnabled}
          />
        </div>
      </div>

      {/* 快捷预设按钮组 */}
      {enabled && (
        <div className='flex flex-wrap items-center justify-between gap-1.5 border-t border-border/50 pt-2.5'>
          <div className='flex flex-wrap items-center gap-1.5'>
            <Button
              type='button'
              size='sm'
              variant='outline'
              className='h-6 gap-1 px-2 text-[11px]'
              disabled={disabled}
              onClick={handlePresetAll}
            >
              <Check className='size-3' />
              {t('schedule.presetAll', '全天')}
            </Button>
            <Button
              type='button'
              size='sm'
              variant='outline'
              className='h-6 gap-1 px-2 text-[11px]'
              disabled={disabled}
              onClick={handlePresetWorkdays}
            >
              <SunMedium className='size-3 text-amber-500' />
              {t('schedule.presetWorkdays', '工作日 (08:00-18:00)')}
            </Button>
            <Button
              type='button'
              size='sm'
              variant='outline'
              className='h-6 gap-1 px-2 text-[11px]'
              disabled={disabled}
              onClick={handlePresetNights}
            >
              <Moon className='size-3 text-indigo-400' />
              {t('schedule.presetNights', '夜间布防 (20:00-06:00)')}
            </Button>
            <Button
              type='button'
              size='sm'
              variant='ghost'
              className='h-6 gap-1 px-2 text-[11px] text-muted-foreground hover:text-destructive'
              disabled={disabled}
              onClick={handlePresetClear}
            >
              <RotateCcw className='size-3' />
              {t('schedule.presetClear', '清空')}
            </Button>
          </div>
          <span className='font-mono text-[10px] text-muted-foreground'>
            {t('schedule.coverage', {
              hours: totalHours,
              percent: coveragePercent,
              defaultValue: `每周运行 ${totalHours} 小时 (${coveragePercent}% 覆盖)`,
            })}
          </span>
        </div>
      )}

      {/* 7x24 网格 */}
      {enabled && (
        <div className='overflow-x-auto rounded-lg border border-border/80 bg-muted/20 p-2.5 select-none'>
          {/* 时间标尺刻度 */}
          <div className='mb-1.5 flex items-center'>
            <div className='w-8 shrink-0 text-center font-mono text-[9px] text-muted-foreground'>
              #
            </div>
            <div className='relative flex flex-1 items-center justify-between px-0.5 font-mono text-[9px] text-muted-foreground'>
              {TIME_MARKS.map((hour) => (
                <span key={hour} className='tabular-nums'>
                  {hour.toString().padStart(2, '0')}:00
                </span>
              ))}
            </div>
          </div>

          {/* 7 天时间行 */}
          <div className='flex flex-col gap-1'>
            {DAY_KEYS.map((day) => {
              const slots = gridState[day]
              const dayLabel = t(
                `schedule.days.${day}`,
                day.slice(0, 3).toUpperCase()
              )
              return (
                <div key={day} className='flex items-center gap-1.5'>
                  <button
                    type='button'
                    title={`点击切换 ${dayLabel} 全天`}
                    disabled={disabled}
                    className='w-8 shrink-0 rounded px-1 py-0.5 text-center font-mono text-[10px] font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground'
                    onClick={() => handleDayLabelClick(day)}
                  >
                    {dayLabel}
                  </button>
                  <div className='flex flex-1 items-center gap-[1px]'>
                    {slots.map((isActive, slotIdx) => {
                      // Add a slightly wider divider every 2 hours (every 4 slots)
                      const isHourBoundary =
                        (slotIdx + 1) % 4 === 0 && slotIdx !== SLOTS_PER_DAY - 1
                      return (
                        <div
                          key={slotIdx}
                          onPointerDown={() => handlePointerDown(day, slotIdx)}
                          onPointerEnter={() =>
                            handlePointerEnter(day, slotIdx)
                          }
                          title={`${dayLabel} ${Math.floor(slotIdx / 2)
                            .toString()
                            .padStart(
                              2,
                              '0'
                            )}:${slotIdx % 2 === 0 ? '00' : '30'}`}
                          className={cn(
                            'h-4.5 flex-1 cursor-pointer transition-colors duration-75',
                            isActive
                              ? 'bg-primary hover:bg-primary/80'
                              : 'border border-border/30 bg-muted/40 hover:bg-muted/80',
                            isHourBoundary && 'mr-0.5',
                            slotIdx === 0 && 'rounded-l-xs',
                            slotIdx === SLOTS_PER_DAY - 1 && 'rounded-r-xs'
                          )}
                        />
                      )
                    })}
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}
