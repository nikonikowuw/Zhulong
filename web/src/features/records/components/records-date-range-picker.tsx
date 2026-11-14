import { useState } from 'react'
import { endOfDay, format } from 'date-fns'
import { Calendar as CalendarIcon, Clock } from 'lucide-react'
import { type DateRange } from 'react-day-picker'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Calendar } from '@/components/ui/calendar'
import { Input } from '@/components/ui/input'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { Separator } from '@/components/ui/separator'
import {
  PERIOD_TIME_PRESETS,
  RECENT_TIME_PRESETS,
  type RecordsDateRange,
  type TimePresetKey,
} from '../data/time-presets'

interface RecordsDateRangePickerProps {
  preset?: TimePresetKey
  startTime?: string
  endTime?: string
  /** 当前窗口就是默认「今日全天」：不算用户筛选，不提供无意义的清空按钮 */
  isDefaultWindow?: boolean
  onRangeChange: (params: RecordsDateRange) => void
}

function parseValidDate(value?: string): Date | undefined {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : date
}

function formatBadgeText(startStr?: string, endStr?: string): string {
  if (!startStr && !endStr) return ''
  const s = parseValidDate(startStr)
  const e = parseValidDate(endStr)
  if (s && e) {
    if (format(s, 'yyyy-MM-dd') === format(e, 'yyyy-MM-dd')) {
      return `${format(s, 'MM-dd HH:mm')} ~ ${format(e, 'HH:mm')}`
    }
    return `${format(s, 'MM-dd HH:mm')} ~ ${format(e, 'MM-dd HH:mm')}`
  }
  if (s) return `>= ${format(s, 'MM-dd HH:mm')}`
  if (e) return `<= ${format(e, 'MM-dd HH:mm')}`
  return ''
}

function buildLocalDateTime(
  dateStr: string,
  timePart: string,
  fallbackTime: string
): Date {
  const candidate = new Date(`${dateStr}T${timePart || fallbackTime}`)
  if (!Number.isNaN(candidate.getTime())) return candidate
  return new Date(`${dateStr}T${fallbackTime}`)
}

export function RecordsDateRangePicker({
  preset,
  startTime,
  endTime,
  isDefaultWindow = false,
  onRangeChange,
}: RecordsDateRangePickerProps) {
  const { t } = useTranslation('records')
  const [open, setOpen] = useState(false)

  const [selectedRange, setSelectedRange] = useState<DateRange | undefined>(
    undefined
  )
  const [startTimePart, setStartTimePart] = useState<string>('00:00:00')
  const [endTimePart, setEndTimePart] = useState<string>('23:59:59')

  const hasRange = Boolean(preset || startTime || endTime)

  let badgeLabel = ''
  if (preset) {
    badgeLabel = t(`toolbar.presets.${preset}`)
  } else if (startTime || endTime) {
    badgeLabel = formatBadgeText(startTime, endTime)
  }

  const handleOpenChange = (nextOpen: boolean) => {
    if (nextOpen) {
      const fromDate = parseValidDate(startTime)
      const toDate = parseValidDate(endTime)
      setSelectedRange(
        fromDate || toDate ? { from: fromDate, to: toDate } : undefined
      )
      setStartTimePart(fromDate ? format(fromDate, 'HH:mm:ss') : '00:00:00')
      setEndTimePart(toDate ? format(toDate, 'HH:mm:ss') : '23:59:59')
    }
    setOpen(nextOpen)
  }

  const handlePresetSelect = (key: TimePresetKey) => {
    onRangeChange({ preset: key })
    setOpen(false)
  }

  const handleCustomApply = () => {
    if (!selectedRange?.from) {
      onRangeChange({})
      setOpen(false)
      return
    }

    const startDateStr = format(selectedRange.from, 'yyyy-MM-dd')
    const endDateStr = format(
      selectedRange.to || selectedRange.from,
      'yyyy-MM-dd'
    )

    let startDate = buildLocalDateTime(startDateStr, startTimePart, '00:00:00')
    let endDate = buildLocalDateTime(endDateStr, endTimePart, '23:59:59')

    if (startDate.getTime() > endDate.getTime()) {
      ;[startDate, endDate] = [endDate, startDate]
    }

    onRangeChange({
      startTime: startDate.toISOString(),
      endTime: endDate.toISOString(),
    })
    setOpen(false)
  }

  const handleClear = () => {
    setSelectedRange(undefined)
    setStartTimePart('00:00:00')
    setEndTimePart('23:59:59')
    onRangeChange({})
    setOpen(false)
  }

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger asChild>
        <Button variant='outline' size='sm' className='h-8 border-dashed'>
          <CalendarIcon className='size-4' />
          <span>{t('toolbar.dateRangePlaceholder')}</span>
          {hasRange && badgeLabel && (
            <>
              <Separator orientation='vertical' className='mx-2 h-4' />
              <Badge
                variant='secondary'
                className='rounded-sm px-1.5 font-mono font-normal'
              >
                {badgeLabel}
              </Badge>
            </>
          )}
        </Button>
      </PopoverTrigger>

      <PopoverContent className='w-auto p-0 shadow-lg' align='start'>
        <div className='flex flex-col md:flex-row'>
          <div className='flex flex-col p-3'>
            <div className='mb-2 flex items-center justify-between gap-2 border-b border-border pb-2'>
              <div className='flex items-center gap-1.5 text-xs text-muted-foreground'>
                <Clock className='size-3.5' />
                <Input
                  type='time'
                  step='1'
                  aria-label={t('toolbar.startTime')}
                  value={startTimePart}
                  onChange={(e) => setStartTimePart(e.target.value)}
                  className='h-7 w-22 bg-background font-mono text-xs'
                />
                <span>~</span>
                <Input
                  type='time'
                  step='1'
                  aria-label={t('toolbar.endTime')}
                  value={endTimePart}
                  onChange={(e) => setEndTimePart(e.target.value)}
                  className='h-7 w-22 bg-background font-mono text-xs'
                />
              </div>

              <div className='flex items-center gap-1'>
                <Button
                  variant='ghost'
                  size='sm'
                  className='h-6 px-1.5 text-[11px] text-muted-foreground'
                  onClick={() => {
                    setStartTimePart('00:00:00')
                    setEndTimePart('23:59:59')
                  }}
                >
                  {t('toolbar.allDay')}
                </Button>
                <Button
                  variant='ghost'
                  size='sm'
                  className='h-6 px-1.5 text-[11px] text-muted-foreground'
                  onClick={() => {
                    setEndTimePart(format(new Date(), 'HH:mm:ss'))
                  }}
                >
                  {t('toolbar.now')}
                </Button>
              </div>
            </div>

            <Calendar
              mode='range'
              selected={selectedRange}
              onSelect={setSelectedRange}
              numberOfMonths={1}
              disabled={(date) => date > endOfDay(new Date())}
              className='p-0'
            />

            <div className='mt-2 flex items-center justify-between border-t border-border pt-2.5'>
              {hasRange && !isDefaultWindow && (
                <Button
                  variant='ghost'
                  size='sm'
                  className='h-7 px-2 text-xs text-muted-foreground hover:text-destructive'
                  onClick={handleClear}
                >
                  {t('toolbar.clearDateRange')}
                </Button>
              )}
              <div className='ms-auto flex items-center gap-1.5'>
                <Button
                  variant='ghost'
                  size='sm'
                  className='h-7 px-2.5 text-xs'
                  onClick={() => setOpen(false)}
                >
                  {t('toolbar.cancel')}
                </Button>
                <Button
                  size='sm'
                  className='h-7 px-3 text-xs'
                  onClick={handleCustomApply}
                  disabled={!selectedRange?.from}
                >
                  {t('toolbar.apply')}
                </Button>
              </div>
            </div>
          </div>

          <div className='flex w-full flex-col gap-1 border-t border-border bg-muted/20 p-3 md:w-36 md:border-s md:border-t-0'>
            <span className='mb-1 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase'>
              {t('toolbar.quickPresets')}
            </span>

            <span className='mt-1 text-[10px] font-medium text-muted-foreground/70'>
              {t('toolbar.presetGroups.recent')}
            </span>
            {RECENT_TIME_PRESETS.map((item) => (
              <Button
                key={item.key}
                variant={preset === item.key ? 'secondary' : 'ghost'}
                size='sm'
                className={`h-7 justify-start px-2 text-xs font-normal ${
                  preset === item.key
                    ? 'font-medium text-foreground'
                    : 'text-muted-foreground hover:text-foreground'
                }`}
                onClick={() => handlePresetSelect(item.key)}
              >
                {t(item.labelKey)}
              </Button>
            ))}

            <span className='mt-2 text-[10px] font-medium text-muted-foreground/70'>
              {t('toolbar.presetGroups.period')}
            </span>
            {PERIOD_TIME_PRESETS.map((item) => (
              <Button
                key={item.key}
                variant={preset === item.key ? 'secondary' : 'ghost'}
                size='sm'
                className={`h-7 justify-start px-2 text-xs font-normal ${
                  preset === item.key
                    ? 'font-medium text-foreground'
                    : 'text-muted-foreground hover:text-foreground'
                }`}
                onClick={() => handlePresetSelect(item.key)}
              >
                {t(item.labelKey)}
              </Button>
            ))}
          </div>
        </div>
      </PopoverContent>
    </Popover>
  )
}
