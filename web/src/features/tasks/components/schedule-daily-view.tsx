import React from 'react'
import { Copy, Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  DAY_KEYS,
  WEEKEND_KEYS,
  WORKDAY_KEYS,
  formatDuration,
  type DayKey,
  type WeekSchedule,
} from '../data/schedule-config'
import { ScheduleTimeInput } from './schedule-time-input'
import { ScheduleTimelineBar } from './schedule-timeline-bar'

export interface ScheduleDailyViewProps {
  schedule: WeekSchedule
  disabled?: boolean
  onToggleDayAll: (day: DayKey) => void
  onClearDay: (day: DayKey) => void
  onCopyDayTo: (fromDay: DayKey, targetDays: readonly DayKey[]) => void
  onUpdateDayRange: (
    day: DayKey,
    index: number,
    field: 'start' | 'end',
    val: string
  ) => void
  onRemoveDayInterval: (day: DayKey, index: number) => void
  onAddDayInterval: (day: DayKey) => void
}

export function ScheduleDailyView({
  schedule,
  disabled = false,
  onToggleDayAll,
  onClearDay,
  onCopyDayTo,
  onUpdateDayRange,
  onRemoveDayInterval,
  onAddDayInterval,
}: ScheduleDailyViewProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  return (
    <div className='flex flex-col gap-2.5'>
      {DAY_KEYS.map((day) => {
        const ranges = schedule.days?.[day] ?? []
        const dayLabel = t(
          `schedule.days.${day}`,
          day.slice(0, 3).toUpperCase()
        )
        const hasRanges = ranges.length > 0

        return (
          <div
            key={day}
            className='flex flex-col gap-2 rounded-md border border-border/60 bg-card p-2.5 shadow-2xs'
          >
            <div className='flex items-center justify-between'>
              <div className='flex items-center gap-2'>
                <button
                  type='button'
                  disabled={disabled}
                  onClick={() => onToggleDayAll(day)}
                  className={cn(
                    'rounded px-2 py-0.5 font-mono text-xs font-semibold transition-colors',
                    hasRanges
                      ? 'bg-primary/10 text-primary'
                      : 'bg-muted text-muted-foreground hover:bg-muted/80'
                  )}
                  title={`点击一键全天反选 ${dayLabel}`}
                >
                  {dayLabel}
                </button>
                <span className='font-mono text-[10px] text-muted-foreground'>
                  {hasRanges
                    ? `${ranges.length} 时段`
                    : t('schedule.dormant', '休眠')}
                </span>
              </div>

              <div className='flex items-center gap-1'>
                {/* 复制菜单 */}
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button
                      type='button'
                      variant='ghost'
                      size='icon'
                      disabled={disabled}
                      className='size-6 text-muted-foreground hover:text-foreground'
                      title={`${t('schedule.copyTo', '复制到')}: ${dayLabel}`}
                    >
                      <Copy className='size-3' />
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align='end' className='w-48 text-xs'>
                    <DropdownMenuItem
                      onClick={() => onCopyDayTo(day, WORKDAY_KEYS)}
                    >
                      {t(
                        'schedule.copyToWorkdays',
                        '复制到工作日 (周一至周五)'
                      )}
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      onClick={() => onCopyDayTo(day, WEEKEND_KEYS)}
                    >
                      {t('schedule.copyToWeekends', '复制到周末 (周六至周日)')}
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      onClick={() => onCopyDayTo(day, DAY_KEYS)}
                    >
                      {t('schedule.copyToAll', '复制到整周 (每天)')}
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>

                <Button
                  type='button'
                  variant='ghost'
                  size='icon'
                  disabled={disabled || !hasRanges}
                  onClick={() => onClearDay(day)}
                  className='size-6 text-muted-foreground hover:text-destructive'
                  title={`清空 ${dayLabel}`}
                >
                  <Trash2 className='size-3' />
                </Button>
              </div>
            </div>

            <ScheduleTimelineBar ranges={ranges} />

            {/* 行内时段输入列表 */}
            <div className='flex flex-col gap-1.5 pt-0.5'>
              {ranges.map((r, idx) => (
                <div
                  key={idx}
                  className='flex items-center justify-between rounded border border-border/40 bg-muted/20 px-2 py-1 text-xs'
                >
                  <div className='flex items-center gap-1.5'>
                    <ScheduleTimeInput
                      value={r.start}
                      disabled={disabled}
                      onChange={(val) =>
                        onUpdateDayRange(day, idx, 'start', val)
                      }
                    />
                    <span className='font-mono text-xs text-muted-foreground'>
                      ~
                    </span>
                    <ScheduleTimeInput
                      value={r.end}
                      disabled={disabled}
                      onChange={(val) => onUpdateDayRange(day, idx, 'end', val)}
                    />
                    <span className='font-mono text-[10px] text-muted-foreground tabular-nums'>
                      ({formatDuration(r.start, r.end)})
                    </span>
                  </div>

                  <Button
                    type='button'
                    variant='ghost'
                    size='icon'
                    disabled={disabled}
                    onClick={() => onRemoveDayInterval(day, idx)}
                    className='size-6 text-muted-foreground hover:text-destructive'
                    title='删除时段'
                  >
                    <Trash2 className='size-3' />
                  </Button>
                </div>
              ))}

              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={disabled}
                onClick={() => onAddDayInterval(day)}
                className='h-6 w-full border-dashed text-[10px] font-medium text-primary hover:bg-primary/5'
              >
                <Plus className='size-3' />
                {t('schedule.addRange', '添加时段')}
              </Button>
            </div>
          </div>
        )
      })}
    </div>
  )
}
