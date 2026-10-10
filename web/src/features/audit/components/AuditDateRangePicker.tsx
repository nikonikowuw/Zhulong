import { useState } from 'react'
import { format } from 'date-fns'
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
  computeRangeFromPreset,
  type AuditDateRange,
  type TimePresetKey,
} from '../data/time-presets'

interface AuditDateRangePickerProps {
  preset?: TimePresetKey
  startTime?: string
  endTime?: string
  onRangeChange: (params: AuditDateRange) => void
}

function parseValidDate(value?: string): Date | undefined {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : date
}

function formatBadgeText(startStr?: string, endStr?: string): string {
  if (!startStr && !endStr) return ''
  try {
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
  } catch {
    return ''
  }
  return ''
}

const recentPresets: { key: TimePresetKey; labelKey: string }[] = [
  { key: '15m', labelKey: 'toolbar.presets.15m' },
  { key: '30m', labelKey: 'toolbar.presets.30m' },
  { key: '1h', labelKey: 'toolbar.presets.1h' },
  { key: '6h', labelKey: 'toolbar.presets.6h' },
  { key: '24h', labelKey: 'toolbar.presets.24h' },
]

const periodPresets: { key: TimePresetKey; labelKey: string }[] = [
  { key: 'today', labelKey: 'toolbar.presets.today' },
  { key: '7d', labelKey: 'toolbar.presets.7d' },
  { key: '30d', labelKey: 'toolbar.presets.30d' },
]

export function AuditDateRangePicker({
  preset,
  startTime,
  endTime,
  onRangeChange,
}: AuditDateRangePickerProps) {
  const { t } = useTranslation('audit')
  const [open, setOpen] = useState(false)

  // 内部日历临时选中状态
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
    const computed = computeRangeFromPreset(key)
    onRangeChange({
      preset: key,
      startTime: computed.startTime,
      endTime: computed.endTime,
    })
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

    const startDate = new Date(`${startDateStr}T${startTimePart || '00:00:00'}`)
    const endDate = new Date(`${endDateStr}T${endTimePart || '23:59:59'}`)

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
          {t('toolbar.dateRangePlaceholder')}
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
          {/* 左侧：日历主体与精细时分微调 */}
          <div className='flex flex-col p-3'>
            {/* 顶部时分精确控制 */}
            <div className='mb-2 flex items-center justify-between gap-2 border-b border-border pb-2'>
              <div className='flex items-center gap-1.5 text-xs text-muted-foreground'>
                <Clock className='size-3.5' />
                <Input
                  type='time'
                  step='1'
                  value={startTimePart}
                  onChange={(e) => setStartTimePart(e.target.value)}
                  className='h-7 w-22 bg-background font-mono text-xs'
                />
                <span>~</span>
                <Input
                  type='time'
                  step='1'
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

            {/* 核心日历 */}
            <Calendar
              initialFocus
              mode='range'
              selected={selectedRange}
              onSelect={setSelectedRange}
              numberOfMonths={1}
              className='p-0'
            />

            {/* 底部操作底栏 */}
            <div className='mt-2 flex items-center justify-between border-t border-border pt-2.5'>
              {hasRange && (
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

          {/* 右侧：经典垂直快捷预设 (Quick Ranges Sidebar) */}
          <div className='flex w-full flex-col gap-1 border-t border-border bg-muted/20 p-3 md:w-36 md:border-s md:border-t-0'>
            <span className='mb-1 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase'>
              {t('toolbar.quickPresets')}
            </span>

            <span className='mt-1 text-[10px] font-medium text-muted-foreground/70'>
              {t('toolbar.presetGroups.recent')}
            </span>
            {recentPresets.map((item) => (
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
            {periodPresets.map((item) => (
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
