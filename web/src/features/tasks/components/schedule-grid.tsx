import React, { useMemo, useState } from 'react'
import { CalendarClock } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  DAY_KEYS,
  MINUTES_PER_DAY,
  WORKDAY_KEYS,
  addIntervalToDay,
  clearDaySchedule,
  copyDaySchedule,
  minutesToTimeString,
  parseSchedule,
  removeIntervalFromDay,
  serializeSchedule,
  timeStringToMinutes,
  updateIntervalInDay,
  type DayKey,
  type TimeRange,
  type WeekSchedule,
} from '../data/schedule-config'
import { ScheduleDailyView } from './schedule-daily-view'
import { ScheduleGroupedView } from './schedule-grouped-view'
import {
  SchedulePresetToolbar,
  type ScheduleViewMode,
} from './schedule-preset-toolbar'

export interface ScheduleGridProps {
  value?: string
  onChange: (scheduleJson: string) => void
  disabled?: boolean
}

export function ScheduleGrid({
  value,
  onChange,
  disabled = false,
}: ScheduleGridProps): React.JSX.Element {
  const { t } = useTranslation('tasks')
  const [viewMode, setViewMode] = useState<ScheduleViewMode>('grouped')

  const schedule = useMemo(() => parseSchedule(value), [value])
  const enabled = schedule.enabled

  const commitSchedule = (next: WeekSchedule) => {
    if (!next.enabled) {
      onChange('{}')
      return
    }
    onChange(serializeSchedule(next))
  }

  // 快捷预设
  const handlePresetAll = () => {
    const allDays: Partial<Record<DayKey, TimeRange[]>> = {}
    for (const day of DAY_KEYS) {
      allDays[day] = [{ start: '00:00', end: '24:00' }]
    }
    commitSchedule({ enabled: true, days: allDays })
  }

  const handleToggleEnabled = (checked: boolean) => {
    if (!checked) {
      onChange('{}')
    } else {
      const hasAny = Object.values(schedule.days ?? {}).some(
        (ranges) => ranges && ranges.length > 0
      )
      if (!hasAny) {
        handlePresetAll()
      } else {
        commitSchedule({ ...schedule, enabled: true })
      }
    }
  }

  const handlePresetWorkdays = () => {
    const days: Partial<Record<DayKey, TimeRange[]>> = {}
    for (const day of WORKDAY_KEYS) {
      days[day] = [{ start: '08:00', end: '18:00' }]
    }
    commitSchedule({ enabled: true, days })
  }

  const handlePresetNights = () => {
    const days: Partial<Record<DayKey, TimeRange[]>> = {}
    for (const day of DAY_KEYS) {
      days[day] = [
        { start: '00:00', end: '06:00' },
        { start: '20:00', end: '24:00' },
      ]
    }
    commitSchedule({ enabled: true, days })
  }

  const handlePresetClear = () => {
    commitSchedule({ enabled: true, days: {} })
  }

  // 单天操作
  const handleAddDayInterval = (day: DayKey) => {
    const current = schedule.days?.[day] ?? []
    let nextStart = '08:00'
    let nextEnd = '18:00'
    if (current.length > 0) {
      const last = current[current.length - 1]
      const lastEndMin = timeStringToMinutes(last.end)
      if (lastEndMin < 22 * 60) {
        nextStart = minutesToTimeString(Math.min(1440, lastEndMin + 60))
        nextEnd = minutesToTimeString(Math.min(1440, lastEndMin + 180))
      }
    }
    commitSchedule(
      addIntervalToDay(schedule, day, {
        start: nextStart,
        end: nextEnd,
      })
    )
  }

  const handleUpdateDayRange = (
    day: DayKey,
    index: number,
    field: 'start' | 'end',
    val: string
  ) => {
    const current = schedule.days?.[day] ?? []
    const target = current[index]
    if (!target) return
    const updatedRange = { ...target, [field]: val }
    commitSchedule(updateIntervalInDay(schedule, day, index, updatedRange))
  }

  const handleRemoveDayInterval = (day: DayKey, index: number) => {
    commitSchedule(removeIntervalFromDay(schedule, day, index))
  }

  const handleClearDay = (day: DayKey) => {
    commitSchedule(clearDaySchedule(schedule, day))
  }

  const handleToggleDayAll = (day: DayKey) => {
    const current = schedule.days?.[day] ?? []
    if (current.length > 0) {
      handleClearDay(day)
    } else {
      commitSchedule(
        addIntervalToDay(schedule, day, {
          start: '00:00',
          end: '24:00',
        })
      )
    }
  }

  const handleCopyDayTo = (fromDay: DayKey, targetDays: readonly DayKey[]) => {
    commitSchedule(copyDaySchedule(schedule, fromDay, [...targetDays]))
  }

  // 分组操作 (工作日 / 周末)
  const handleAddGroupInterval = (targetDays: readonly DayKey[]) => {
    let next = { ...schedule }
    for (const d of targetDays) {
      next = addIntervalToDay(next, d, { start: '08:00', end: '18:00' })
    }
    commitSchedule(next)
  }

  const handleUpdateGroupRange = (
    targetDays: readonly DayKey[],
    index: number,
    field: 'start' | 'end',
    val: string
  ) => {
    let next = { ...schedule }
    for (const d of targetDays) {
      const ranges = next.days?.[d] ?? []
      if (ranges[index]) {
        const updatedRange = { ...ranges[index], [field]: val }
        next = updateIntervalInDay(next, d, index, updatedRange)
      }
    }
    commitSchedule(next)
  }

  const handleRemoveGroupInterval = (
    targetDays: readonly DayKey[],
    index: number
  ) => {
    let next = { ...schedule }
    for (const d of targetDays) {
      next = removeIntervalFromDay(next, d, index)
    }
    commitSchedule(next)
  }

  const handleClearGroup = (targetDays: readonly DayKey[]) => {
    let next = { ...schedule }
    for (const d of targetDays) {
      next = clearDaySchedule(next, d)
    }
    commitSchedule(next)
  }

  const handleToggleGroupAll = (targetDays: readonly DayKey[]) => {
    const hasAny = targetDays.some((d) => (schedule.days?.[d] ?? []).length > 0)
    if (hasAny) {
      handleClearGroup(targetDays)
    } else {
      let next = { ...schedule }
      for (const d of targetDays) {
        next = addIntervalToDay(next, d, { start: '00:00', end: '24:00' })
      }
      commitSchedule(next)
    }
  }

  // 统计覆盖时间
  const totalWeeklyMinutes = useMemo(() => {
    if (!enabled) return 7 * MINUTES_PER_DAY
    let total = 0
    for (const day of DAY_KEYS) {
      const ranges = schedule.days?.[day] ?? []
      for (const r of ranges) {
        const s = timeStringToMinutes(r.start)
        const e = timeStringToMinutes(r.end)
        if (e > s) total += e - s
      }
    }
    return total
  }, [enabled, schedule])

  const totalHours = (totalWeeklyMinutes / 60).toFixed(1)
  const coveragePercent = Math.min(
    100,
    Math.round((totalWeeklyMinutes / (7 * MINUTES_PER_DAY)) * 100)
  )

  return (
    <div className='flex flex-1 flex-col gap-3 rounded-xl border border-border bg-card p-4 shadow-xs'>
      {/* 头部标题与总开关 */}
      <div className='flex items-center justify-between'>
        <div className='flex items-center gap-2'>
          <CalendarClock className='size-4 text-primary' />
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
              {t(
                'schedule.coverage',
                '每周运行 {{hours}} 小时 ({{percent}}% 覆盖)',
                {
                  hours: totalHours,
                  percent: coveragePercent,
                }
              )}
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

      {/* 快捷模板工具条与视图切换 */}
      {enabled && (
        <SchedulePresetToolbar
          disabled={disabled}
          viewMode={viewMode}
          onViewModeChange={setViewMode}
          onPresetAll={handlePresetAll}
          onPresetWorkdays={handlePresetWorkdays}
          onPresetNights={handlePresetNights}
          onPresetClear={handlePresetClear}
        />
      )}

      {/* 主体排程交互区域 */}
      {enabled && (
        <div className='flex flex-1 flex-col gap-3 rounded-lg border border-border/80 bg-muted/20 p-3'>
          {/* 时间标尺刻度 */}
          <div className='flex items-center justify-between px-1 font-mono text-[9px] text-muted-foreground'>
            <span>00:00</span>
            <span>06:00</span>
            <span>12:00</span>
            <span>18:00</span>
            <span>24:00</span>
          </div>

          {viewMode === 'grouped' ? (
            <ScheduleGroupedView
              schedule={schedule}
              disabled={disabled}
              onToggleGroupAll={handleToggleGroupAll}
              onClearGroup={handleClearGroup}
              onUpdateGroupRange={handleUpdateGroupRange}
              onRemoveGroupInterval={handleRemoveGroupInterval}
              onAddGroupInterval={handleAddGroupInterval}
            />
          ) : (
            <ScheduleDailyView
              schedule={schedule}
              disabled={disabled}
              onToggleDayAll={handleToggleDayAll}
              onClearDay={handleClearDay}
              onCopyDayTo={handleCopyDayTo}
              onUpdateDayRange={handleUpdateDayRange}
              onRemoveDayInterval={handleRemoveDayInterval}
              onAddDayInterval={handleAddDayInterval}
            />
          )}
        </div>
      )}
    </div>
  )
}
