import { useMemo, useState } from 'react'
import {
  CalendarClock,
  Check,
  Copy,
  Moon,
  Plus,
  RotateCcw,
  SunMedium,
  Trash2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  DAY_KEYS,
  MINUTES_PER_DAY,
  WEEKEND_KEYS,
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

export interface ScheduleGridProps {
  value?: string
  onChange: (scheduleJson: string) => void
  disabled?: boolean
}

type ViewMode = 'grouped' | 'daily'

/** 紧凑平滑的分钟级时间轴条 */
function SmoothTimelineBar({
  ranges,
}: {
  ranges: TimeRange[]
}): React.JSX.Element {
  return (
    <div className='relative h-2 w-full overflow-hidden rounded-full bg-muted/60'>
      {/* 4 等分参考刻度点 (06:00, 12:00, 18:00) */}
      <div className='pointer-events-none absolute inset-0 flex justify-between px-[25%]'>
        <div className='h-full w-[1px] bg-background/50' />
        <div className='h-full w-[1px] bg-background/50' />
      </div>

      {/* 连续生效时段高亮 (精确到分钟) */}
      {ranges.map((r, idx) => {
        const startMin = timeStringToMinutes(r.start)
        const endMin = timeStringToMinutes(r.end)
        if (startMin >= endMin) return null
        const left = (startMin / MINUTES_PER_DAY) * 100
        const width = ((endMin - startMin) / MINUTES_PER_DAY) * 100
        return (
          <div
            key={idx}
            className='absolute top-0 bottom-0 rounded-full bg-primary transition-all duration-150'
            style={{
              left: `${left}%`,
              width: `${Math.max(1, width)}%`,
            }}
          />
        )
      })}
    </div>
  )
}

/** 24小时制精细时间输入框 (无 spinner 截断，支持键盘任意打字与自动纠错) */
function TimeInput24h({
  value,
  onChange,
  disabled = false,
}: {
  value: string
  onChange: (val: string) => void
  disabled?: boolean
}): React.JSX.Element {
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

function formatDuration(start: string, end: string): string {
  const s = timeStringToMinutes(start)
  const e = timeStringToMinutes(end)
  if (e <= s) return '跨天'
  const diffHours = ((e - s) / 60).toFixed(1)
  return `${diffHours}h`
}

export function ScheduleGrid({
  value,
  onChange,
  disabled = false,
}: ScheduleGridProps): React.JSX.Element {
  const { t } = useTranslation('tasks')
  const [viewMode, setViewMode] = useState<ViewMode>('grouped')

  const schedule = useMemo(() => parseSchedule(value), [value])
  const enabled = schedule.enabled

  const commitSchedule = (next: WeekSchedule) => {
    if (!next.enabled) {
      onChange('{}')
      return
    }
    onChange(serializeSchedule(next))
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

  // 快捷预设
  const handlePresetAll = () => {
    const allDays: Partial<Record<DayKey, TimeRange[]>> = {}
    for (const day of DAY_KEYS) {
      allDays[day] = [{ start: '00:00', end: '24:00' }]
    }
    commitSchedule({ enabled: true, days: allDays })
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
    // 智能建议下一个时段起点
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
    const updated = addIntervalToDay(schedule, day, {
      start: nextStart,
      end: nextEnd,
    })
    commitSchedule(updated)
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
    const updated = updateIntervalInDay(schedule, day, index, updatedRange)
    commitSchedule(updated)
  }

  const handleRemoveDayInterval = (day: DayKey, index: number) => {
    const updated = removeIntervalFromDay(schedule, day, index)
    commitSchedule(updated)
  }

  const handleClearDay = (day: DayKey) => {
    const updated = clearDaySchedule(schedule, day)
    commitSchedule(updated)
  }

  const handleToggleDayAll = (day: DayKey) => {
    const current = schedule.days?.[day] ?? []
    if (current.length > 0) {
      handleClearDay(day)
    } else {
      const updated = addIntervalToDay(schedule, day, {
        start: '00:00',
        end: '24:00',
      })
      commitSchedule(updated)
    }
  }

  const handleCopyDayTo = (fromDay: DayKey, targetDays: readonly DayKey[]) => {
    const updated = copyDaySchedule(schedule, fromDay, [...targetDays])
    commitSchedule(updated)
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

  const workdayRanges = schedule.days?.monday ?? []
  const weekendRanges = schedule.days?.saturday ?? []

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
              {coveragePercent}% 覆盖 ({totalHours}h/周)
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

          {/* 模式切换 (快捷分组 / 7天明细) */}
          <div className='flex items-center rounded-md border border-border/60 bg-muted/40 p-0.5 text-[10px] font-medium'>
            <button
              type='button'
              onClick={() => setViewMode('grouped')}
              className={cn(
                'rounded px-2.5 py-0.5 transition-colors',
                viewMode === 'grouped'
                  ? 'bg-background font-semibold text-foreground shadow-xs'
                  : 'text-muted-foreground hover:text-foreground'
              )}
            >
              {t('schedule.modeGrouped', '工作日/周末')}
            </button>
            <button
              type='button'
              onClick={() => setViewMode('daily')}
              className={cn(
                'rounded px-2.5 py-0.5 transition-colors',
                viewMode === 'daily'
                  ? 'bg-background font-semibold text-foreground shadow-xs'
                  : 'text-muted-foreground hover:text-foreground'
              )}
            >
              {t('schedule.modeDaily', '按天明细')}
            </button>
          </div>
        </div>
      )}

      {/* 主体排程交互区域 (完全内嵌、所见即所得、零弹层阻断) */}
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

          {/* 模式 A：工作日 / 周末 快捷分组模式 */}
          {viewMode === 'grouped' && (
            <div className='flex flex-col gap-3'>
              {/* 工作日卡片 */}
              <div className='flex flex-col gap-2 rounded-lg border border-border/70 bg-card p-3 shadow-2xs'>
                <div className='flex items-center justify-between'>
                  <div className='flex items-center gap-2'>
                    <button
                      type='button'
                      disabled={disabled}
                      onClick={() => handleToggleGroupAll(WORKDAY_KEYS)}
                      className='rounded-md bg-muted px-2 py-1 font-mono text-xs font-semibold text-foreground hover:bg-muted/80'
                      title='点击切换工作日全天/清空'
                    >
                      {t('schedule.workdays', '工作日 (周一至周五)')}
                    </button>
                    <span className='font-mono text-[10px] text-muted-foreground'>
                      {workdayRanges.length > 0
                        ? `${workdayRanges.length} 个时段`
                        : t('schedule.dormant', '休眠')}
                    </span>
                  </div>

                  <Button
                    type='button'
                    variant='ghost'
                    size='icon'
                    disabled={disabled || workdayRanges.length === 0}
                    onClick={() => handleClearGroup(WORKDAY_KEYS)}
                    className='size-6 text-muted-foreground hover:text-destructive'
                    title='清空工作日时段'
                  >
                    <Trash2 className='size-3.5' />
                  </Button>
                </div>

                <SmoothTimelineBar ranges={workdayRanges} />

                {/* 行内时段输入列表 (直接手打修改分钟，零遮挡) */}
                <div className='flex flex-col gap-1.5 pt-1'>
                  {workdayRanges.map((r, idx) => (
                    <div
                      key={idx}
                      className='flex items-center justify-between rounded-md border border-border/50 bg-muted/30 px-2 py-1 text-xs'
                    >
                      <div className='flex items-center gap-1.5'>
                        <TimeInput24h
                          value={r.start}
                          disabled={disabled}
                          onChange={(val) =>
                            handleUpdateGroupRange(
                              WORKDAY_KEYS,
                              idx,
                              'start',
                              val
                            )
                          }
                        />
                        <span className='font-mono text-xs text-muted-foreground'>
                          ~
                        </span>
                        <TimeInput24h
                          value={r.end}
                          disabled={disabled}
                          onChange={(val) =>
                            handleUpdateGroupRange(
                              WORKDAY_KEYS,
                              idx,
                              'end',
                              val
                            )
                          }
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
                        onClick={() =>
                          handleRemoveGroupInterval(WORKDAY_KEYS, idx)
                        }
                        className='size-6 text-muted-foreground hover:text-destructive'
                        title='删除时段'
                      >
                        <Trash2 className='size-3' />
                      </Button>
                    </div>
                  ))}

                  {/* 添加时段虚线按钮 */}
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    disabled={disabled}
                    onClick={() => handleAddGroupInterval(WORKDAY_KEYS)}
                    className='h-7 w-full border-dashed text-[11px] font-medium text-primary hover:bg-primary/5'
                  >
                    <Plus className='size-3' />
                    {t('schedule.addRange', '添加工作日时段')}
                  </Button>
                </div>
              </div>

              {/* 周末卡片 */}
              <div className='flex flex-col gap-2 rounded-lg border border-border/70 bg-card p-3 shadow-2xs'>
                <div className='flex items-center justify-between'>
                  <div className='flex items-center gap-2'>
                    <button
                      type='button'
                      disabled={disabled}
                      onClick={() => handleToggleGroupAll(WEEKEND_KEYS)}
                      className='rounded-md bg-muted px-2 py-1 font-mono text-xs font-semibold text-foreground hover:bg-muted/80'
                      title='点击切换周末全天/清空'
                    >
                      {t('schedule.weekends', '周末 (周六至周日)')}
                    </button>
                    <span className='font-mono text-[10px] text-muted-foreground'>
                      {weekendRanges.length > 0
                        ? `${weekendRanges.length} 个时段`
                        : t('schedule.dormant', '休眠')}
                    </span>
                  </div>

                  <Button
                    type='button'
                    variant='ghost'
                    size='icon'
                    disabled={disabled || weekendRanges.length === 0}
                    onClick={() => handleClearGroup(WEEKEND_KEYS)}
                    className='size-6 text-muted-foreground hover:text-destructive'
                    title='清空周末时段'
                  >
                    <Trash2 className='size-3.5' />
                  </Button>
                </div>

                <SmoothTimelineBar ranges={weekendRanges} />

                {/* 行内时段输入列表 */}
                <div className='flex flex-col gap-1.5 pt-1'>
                  {weekendRanges.map((r, idx) => (
                    <div
                      key={idx}
                      className='flex items-center justify-between rounded-md border border-border/50 bg-muted/30 px-2 py-1 text-xs'
                    >
                      <div className='flex items-center gap-1.5'>
                        <TimeInput24h
                          value={r.start}
                          disabled={disabled}
                          onChange={(val) =>
                            handleUpdateGroupRange(
                              WEEKEND_KEYS,
                              idx,
                              'start',
                              val
                            )
                          }
                        />
                        <span className='font-mono text-xs text-muted-foreground'>
                          ~
                        </span>
                        <TimeInput24h
                          value={r.end}
                          disabled={disabled}
                          onChange={(val) =>
                            handleUpdateGroupRange(
                              WEEKEND_KEYS,
                              idx,
                              'end',
                              val
                            )
                          }
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
                        onClick={() =>
                          handleRemoveGroupInterval(WEEKEND_KEYS, idx)
                        }
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
                    onClick={() => handleAddGroupInterval(WEEKEND_KEYS)}
                    className='h-7 w-full border-dashed text-[11px] font-medium text-primary hover:bg-primary/5'
                  >
                    <Plus className='size-3' />
                    {t('schedule.addRange', '添加周末时段')}
                  </Button>
                </div>
              </div>
            </div>
          )}

          {/* 模式 B：7 天明细独立配置 (直接行内展开，随时修改) */}
          {viewMode === 'daily' && (
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
                          onClick={() => handleToggleDayAll(day)}
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
                          <DropdownMenuContent
                            align='end'
                            className='w-48 text-xs'
                          >
                            <DropdownMenuItem
                              onClick={() => handleCopyDayTo(day, WORKDAY_KEYS)}
                            >
                              {t(
                                'schedule.copyToWorkdays',
                                '复制到工作日 (周一至周五)'
                              )}
                            </DropdownMenuItem>
                            <DropdownMenuItem
                              onClick={() => handleCopyDayTo(day, WEEKEND_KEYS)}
                            >
                              {t(
                                'schedule.copyToWeekends',
                                '复制到周末 (周六至周日)'
                              )}
                            </DropdownMenuItem>
                            <DropdownMenuItem
                              onClick={() => handleCopyDayTo(day, DAY_KEYS)}
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
                          onClick={() => handleClearDay(day)}
                          className='size-6 text-muted-foreground hover:text-destructive'
                          title={`清空 ${dayLabel}`}
                        >
                          <Trash2 className='size-3' />
                        </Button>
                      </div>
                    </div>

                    <SmoothTimelineBar ranges={ranges} />

                    {/* 行内时段输入列表 */}
                    <div className='flex flex-col gap-1.5 pt-0.5'>
                      {ranges.map((r, idx) => (
                        <div
                          key={idx}
                          className='flex items-center justify-between rounded border border-border/40 bg-muted/20 px-2 py-1 text-xs'
                        >
                          <div className='flex items-center gap-1.5'>
                            <TimeInput24h
                              value={r.start}
                              disabled={disabled}
                              onChange={(val) =>
                                handleUpdateDayRange(day, idx, 'start', val)
                              }
                            />
                            <span className='font-mono text-xs text-muted-foreground'>
                              ~
                            </span>
                            <TimeInput24h
                              value={r.end}
                              disabled={disabled}
                              onChange={(val) =>
                                handleUpdateDayRange(day, idx, 'end', val)
                              }
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
                            onClick={() => handleRemoveDayInterval(day, idx)}
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
                        onClick={() => handleAddDayInterval(day)}
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
          )}
        </div>
      )}
    </div>
  )
}
