import React from 'react'
import { Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  WEEKEND_KEYS,
  WORKDAY_KEYS,
  formatDuration,
  type DayKey,
  type TimeRange,
  type WeekSchedule,
} from '../data/schedule-config'
import { ScheduleTimeInput } from './schedule-time-input'
import { ScheduleTimelineBar } from './schedule-timeline-bar'

export interface ScheduleGroupedViewProps {
  schedule: WeekSchedule
  disabled?: boolean
  onToggleGroupAll: (targetDays: readonly DayKey[]) => void
  onClearGroup: (targetDays: readonly DayKey[]) => void
  onUpdateGroupRange: (
    targetDays: readonly DayKey[],
    index: number,
    field: 'start' | 'end',
    val: string
  ) => void
  onRemoveGroupInterval: (targetDays: readonly DayKey[], index: number) => void
  onAddGroupInterval: (targetDays: readonly DayKey[]) => void
}

interface GroupCardProps {
  title: string
  targetDays: readonly DayKey[]
  ranges: TimeRange[]
  disabled?: boolean
  addLabel: string
  toggleTitle: string
  clearTitle: string
  onToggleAll: () => void
  onClear: () => void
  onUpdateRange: (index: number, field: 'start' | 'end', val: string) => void
  onRemoveInterval: (index: number) => void
  onAddInterval: () => void
}

function GroupCard({
  title,
  ranges,
  disabled = false,
  addLabel,
  toggleTitle,
  clearTitle,
  onToggleAll,
  onClear,
  onUpdateRange,
  onRemoveInterval,
  onAddInterval,
}: GroupCardProps) {
  const { t } = useTranslation('tasks')

  return (
    <div className='flex flex-col gap-2 rounded-lg border border-border/70 bg-card p-3 shadow-2xs'>
      <div className='flex items-center justify-between'>
        <div className='flex items-center gap-2'>
          <button
            type='button'
            disabled={disabled}
            onClick={onToggleAll}
            className='rounded-md bg-muted px-2 py-1 font-mono text-xs font-semibold text-foreground hover:bg-muted/80'
            title={toggleTitle}
          >
            {title}
          </button>
          <span className='font-mono text-[10px] text-muted-foreground'>
            {ranges.length > 0
              ? t('schedule.rangeCount', {
                  count: ranges.length,
                  defaultValue: `${ranges.length} 个时段`,
                })
              : t('schedule.dormant', '休眠')}
          </span>
        </div>

        <Button
          type='button'
          variant='ghost'
          size='icon'
          disabled={disabled || ranges.length === 0}
          onClick={onClear}
          className='size-6 text-muted-foreground hover:text-destructive'
          title={clearTitle}
        >
          <Trash2 className='size-3.5' />
        </Button>
      </div>

      <ScheduleTimelineBar ranges={ranges} />

      {/* 行内时段输入列表 */}
      <div className='flex flex-col gap-1.5 pt-1'>
        {ranges.map((r, idx) => (
          <div
            key={`range-${idx}`}
            className='flex items-center justify-between rounded-md border border-border/50 bg-muted/30 px-2 py-1 text-xs'
          >
            <div className='flex items-center gap-1.5'>
              <ScheduleTimeInput
                value={r.start}
                disabled={disabled}
                onChange={(val) => onUpdateRange(idx, 'start', val)}
              />
              <span className='font-mono text-xs text-muted-foreground'>~</span>
              <ScheduleTimeInput
                value={r.end}
                disabled={disabled}
                onChange={(val) => onUpdateRange(idx, 'end', val)}
              />
              <span className='font-mono text-[10px] text-muted-foreground tabular-nums'>
                (
                {formatDuration(r.start, r.end, t('schedule.crossDay', '跨天'))}
                )
              </span>
            </div>

            <Button
              type='button'
              variant='ghost'
              size='icon'
              disabled={disabled}
              onClick={() => onRemoveInterval(idx)}
              className='size-6 text-muted-foreground hover:text-destructive'
              title={t('schedule.removeRange', '删除时段')}
              aria-label={t('schedule.removeRange', '删除时段')}
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
          onClick={onAddInterval}
          className='h-7 w-full border-dashed text-[11px] font-medium text-primary hover:bg-primary/5'
        >
          <Plus className='size-3' />
          {addLabel}
        </Button>
      </div>
    </div>
  )
}

export function ScheduleGroupedView({
  schedule,
  disabled = false,
  onToggleGroupAll,
  onClearGroup,
  onUpdateGroupRange,
  onRemoveGroupInterval,
  onAddGroupInterval,
}: ScheduleGroupedViewProps): React.JSX.Element {
  const { t } = useTranslation('tasks')
  const workdayRanges = schedule.days?.monday ?? []
  const weekendRanges = schedule.days?.saturday ?? []

  return (
    <div className='flex flex-col gap-3'>
      <GroupCard
        title={t('schedule.workdays', '工作日 (周一至周五)')}
        targetDays={WORKDAY_KEYS}
        ranges={workdayRanges}
        disabled={disabled}
        addLabel={t('schedule.addWorkdayRange', '添加工作日时段')}
        toggleTitle={t(
          'schedule.toggleWorkdaysTitle',
          '点击切换工作日全天/清空'
        )}
        clearTitle={t('schedule.clearWorkdaysTitle', '清空工作日时段')}
        onToggleAll={() => onToggleGroupAll(WORKDAY_KEYS)}
        onClear={() => onClearGroup(WORKDAY_KEYS)}
        onUpdateRange={(idx, field, val) =>
          onUpdateGroupRange(WORKDAY_KEYS, idx, field, val)
        }
        onRemoveInterval={(idx) => onRemoveGroupInterval(WORKDAY_KEYS, idx)}
        onAddInterval={() => onAddGroupInterval(WORKDAY_KEYS)}
      />

      <GroupCard
        title={t('schedule.weekends', '周末 (周六至周日)')}
        targetDays={WEEKEND_KEYS}
        ranges={weekendRanges}
        disabled={disabled}
        addLabel={t('schedule.addWeekendRange', '添加周末时段')}
        toggleTitle={t('schedule.toggleWeekendsTitle', '点击切换周末全天/清空')}
        clearTitle={t('schedule.clearWeekendsTitle', '清空周末时段')}
        onToggleAll={() => onToggleGroupAll(WEEKEND_KEYS)}
        onClear={() => onClearGroup(WEEKEND_KEYS)}
        onUpdateRange={(idx, field, val) =>
          onUpdateGroupRange(WEEKEND_KEYS, idx, field, val)
        }
        onRemoveInterval={(idx) => onRemoveGroupInterval(WEEKEND_KEYS, idx)}
        onAddInterval={() => onAddGroupInterval(WEEKEND_KEYS)}
      />
    </div>
  )
}
