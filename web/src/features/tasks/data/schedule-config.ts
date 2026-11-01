export const MINUTES_PER_DAY = 1440
export const MINUTES_PER_WEEK = 7 * MINUTES_PER_DAY
export const SLOTS_PER_DAY = 48
export const MINUTES_PER_SLOT = 30

export const DAY_KEYS = [
  'monday',
  'tuesday',
  'wednesday',
  'thursday',
  'friday',
  'saturday',
  'sunday',
] as const

export type DayKey = (typeof DAY_KEYS)[number]

export const WORKDAY_KEYS: readonly DayKey[] = [
  'monday',
  'tuesday',
  'wednesday',
  'thursday',
  'friday',
] as const

export const WEEKEND_KEYS: readonly DayKey[] = ['saturday', 'sunday'] as const

export const HALF_HOUR_OPTIONS: readonly string[] = Array.from(
  { length: 49 },
  (_, i) => {
    const h = Math.floor(i / 2)
    const m = i % 2 === 0 ? '00' : '30'
    return `${h.toString().padStart(2, '0')}:${m}`
  }
)

export interface TimeRange {
  start: string // "08:30"
  end: string // "17:30"
}

export interface WeekSchedule {
  enabled: boolean
  days?: Partial<Record<DayKey, TimeRange[]>>
}

export interface SchedulableItem {
  points: number
  schedule?: WeekSchedule | string
}

export function parseSchedule(raw?: string | null): WeekSchedule {
  if (!raw || raw.trim() === '' || raw.trim() === '{}') {
    return { enabled: false, days: {} }
  }
  try {
    const parsed = JSON.parse(raw) as WeekSchedule
    if (typeof parsed !== 'object' || parsed === null) {
      return { enabled: false, days: {} }
    }
    return {
      enabled: Boolean(parsed.enabled),
      days: parsed.days ?? {},
    }
  } catch {
    return { enabled: false, days: {} }
  }
}

export function serializeSchedule(schedule: WeekSchedule): string {
  if (!schedule.enabled) {
    return '{}'
  }
  return JSON.stringify(schedule)
}

export function isScheduleAlwaysActive(schedule?: WeekSchedule): boolean {
  if (!schedule || !schedule.enabled) return true
  return false
}

export const DAY_KEYS_BY_WEEKDAY: readonly DayKey[] = [
  'sunday',
  'monday',
  'tuesday',
  'wednesday',
  'thursday',
  'friday',
  'saturday',
] as const

export function isScheduleActiveAt(
  schedule?: WeekSchedule | string | null,
  date = new Date()
): boolean {
  const sched =
    typeof schedule === 'string'
      ? parseSchedule(schedule)
      : (schedule ?? { enabled: false })

  if (isScheduleAlwaysActive(sched)) {
    return true
  }

  const weekday = date.getDay()
  const dayKey = DAY_KEYS_BY_WEEKDAY[weekday]
  const ranges = sched.days?.[dayKey]
  if (!ranges || ranges.length === 0) {
    return false
  }

  const currentMinute = date.getHours() * 60 + date.getMinutes()
  for (const r of ranges) {
    const startMin = timeStringToMinutes(r.start)
    const endMin = timeStringToMinutes(r.end)
    if (currentMinute >= startMin && currentMinute < endMin) {
      return true
    }
  }

  return false
}

export function timeStringToMinutes(timeStr: string): number {
  const parts = timeStr.trim().split(':')
  if (parts.length < 2) return 0
  const hour = Number.parseInt(parts[0], 10) || 0
  const min = Number.parseInt(parts[1], 10) || 0
  if (hour === 24 && min === 0) return MINUTES_PER_DAY
  return Math.min(MINUTES_PER_DAY, Math.max(0, hour * 60 + min))
}

export function minutesToTimeString(minutes: number): string {
  if (minutes >= MINUTES_PER_DAY) return '24:00'
  const h = Math.floor(minutes / 60)
  const m = minutes % 60
  return `${h.toString().padStart(2, '0')}:${m.toString().padStart(2, '0')}`
}

export function intervalsToSlots(ranges: TimeRange[] = []): boolean[] {
  const slots = new Array<boolean>(SLOTS_PER_DAY).fill(false)
  for (const r of ranges) {
    const startMin = timeStringToMinutes(r.start)
    const endMin = timeStringToMinutes(r.end)
    const startSlot = Math.floor(startMin / MINUTES_PER_SLOT)
    const endSlot = Math.min(
      SLOTS_PER_DAY,
      Math.ceil(endMin / MINUTES_PER_SLOT)
    )
    for (let i = startSlot; i < endSlot; i++) {
      slots[i] = true
    }
  }
  return slots
}

export function slotsToIntervals(slots: boolean[]): TimeRange[] {
  const ranges: TimeRange[] = []
  let inRange = false
  let startSlot = 0

  for (let i = 0; i < SLOTS_PER_DAY; i++) {
    if (slots[i] && !inRange) {
      inRange = true
      startSlot = i
    } else if (!slots[i] && inRange) {
      inRange = false
      ranges.push({
        start: minutesToTimeString(startSlot * MINUTES_PER_SLOT),
        end: minutesToTimeString(i * MINUTES_PER_SLOT),
      })
    }
  }

  if (inRange) {
    ranges.push({
      start: minutesToTimeString(startSlot * MINUTES_PER_SLOT),
      end: minutesToTimeString(SLOTS_PER_DAY * MINUTES_PER_SLOT),
    })
  }

  return ranges
}

export function copyDaySchedule(
  schedule: WeekSchedule,
  fromDay: DayKey,
  targetDays: DayKey[]
): WeekSchedule {
  const sourceRanges = schedule.days?.[fromDay] ?? []
  const newDays = { ...schedule.days }
  for (const target of targetDays) {
    if (sourceRanges.length === 0) {
      delete newDays[target]
    } else {
      newDays[target] = sourceRanges.map((r) => ({ ...r }))
    }
  }
  return {
    ...schedule,
    days: newDays,
  }
}

export function clearDaySchedule(
  schedule: WeekSchedule,
  day: DayKey
): WeekSchedule {
  const newDays = { ...schedule.days }
  delete newDays[day]
  return {
    ...schedule,
    days: newDays,
  }
}

export function mergeTimeRanges(ranges: TimeRange[]): TimeRange[] {
  if (!ranges || ranges.length === 0) return []
  const intervals = ranges
    .map((r) => ({
      start: timeStringToMinutes(r.start),
      end: timeStringToMinutes(r.end),
    }))
    .filter((iv) => iv.end > iv.start)

  if (intervals.length === 0) return []

  intervals.sort((a, b) => a.start - b.start)

  const merged: Array<{ start: number; end: number }> = [intervals[0]]
  for (let i = 1; i < intervals.length; i++) {
    const current = intervals[i]
    const last = merged[merged.length - 1]

    if (current.start <= last.end) {
      last.end = Math.max(last.end, current.end)
    } else {
      merged.push(current)
    }
  }

  return merged.map((iv) => ({
    start: minutesToTimeString(iv.start),
    end: minutesToTimeString(iv.end),
  }))
}

export function addIntervalToDay(
  schedule: WeekSchedule,
  day: DayKey,
  newRange: TimeRange
): WeekSchedule {
  const currentRanges = schedule.days?.[day] ?? []
  const startMin = timeStringToMinutes(newRange.start)
  const endMin = timeStringToMinutes(newRange.end)

  let addedRanges: TimeRange[]
  if (startMin < endMin) {
    addedRanges = [
      ...currentRanges,
      { start: newRange.start, end: newRange.end },
    ]
  } else if (startMin > endMin) {
    // 跨天支持：例如 20:45 - 06:15
    addedRanges = [
      ...currentRanges,
      { start: newRange.start, end: '24:00' },
      { start: '00:00', end: newRange.end },
    ]
  } else {
    return schedule
  }

  const merged = mergeTimeRanges(addedRanges)
  const newDays = { ...schedule.days }
  if (merged.length > 0) {
    newDays[day] = merged
  } else {
    delete newDays[day]
  }
  return {
    ...schedule,
    days: newDays,
  }
}

export function updateIntervalInDay(
  schedule: WeekSchedule,
  day: DayKey,
  index: number,
  updatedRange: TimeRange
): WeekSchedule {
  const currentRanges = schedule.days?.[day] ?? []
  if (index < 0 || index >= currentRanges.length) return schedule
  const nextRanges = currentRanges.filter((_, idx) => idx !== index)
  const startMin = timeStringToMinutes(updatedRange.start)
  const endMin = timeStringToMinutes(updatedRange.end)

  let addedRanges: TimeRange[]
  if (startMin < endMin) {
    addedRanges = [
      ...nextRanges,
      { start: updatedRange.start, end: updatedRange.end },
    ]
  } else if (startMin > endMin) {
    addedRanges = [
      ...nextRanges,
      { start: updatedRange.start, end: '24:00' },
      { start: '00:00', end: updatedRange.end },
    ]
  } else {
    addedRanges = nextRanges
  }

  const merged = mergeTimeRanges(addedRanges)
  const newDays = { ...schedule.days }
  if (merged.length > 0) {
    newDays[day] = merged
  } else {
    delete newDays[day]
  }
  return {
    ...schedule,
    days: newDays,
  }
}

export function removeIntervalFromDay(
  schedule: WeekSchedule,
  day: DayKey,
  index: number
): WeekSchedule {
  const currentRanges = schedule.days?.[day] ?? []
  if (index < 0 || index >= currentRanges.length) return schedule
  const nextRanges = currentRanges.filter((_, idx) => idx !== index)
  const newDays = { ...schedule.days }
  if (nextRanges.length > 0) {
    newDays[day] = nextRanges
  } else {
    delete newDays[day]
  }
  return {
    ...schedule,
    days: newDays,
  }
}

const DAY_INDEX_MAP: Record<DayKey, number> = {
  sunday: 0,
  monday: 1,
  tuesday: 2,
  wednesday: 3,
  thursday: 4,
  friday: 5,
  saturday: 6,
}

interface SweepEvent {
  minute: number
  delta: number
}

export function calculatePeakCompute(items: SchedulableItem[]): number {
  let basePoints = 0
  const events: SweepEvent[] = []

  for (const item of items) {
    if (item.points <= 0) continue
    const sched =
      typeof item.schedule === 'string'
        ? parseSchedule(item.schedule)
        : (item.schedule ?? { enabled: false })

    if (isScheduleAlwaysActive(sched)) {
      basePoints += item.points
      continue
    }

    for (const day of DAY_KEYS) {
      const ranges = sched.days?.[day] ?? []
      const dayOffset = DAY_INDEX_MAP[day] * MINUTES_PER_DAY
      for (const r of ranges) {
        const start = dayOffset + timeStringToMinutes(r.start)
        const end = dayOffset + timeStringToMinutes(r.end)
        if (start < end) {
          events.push({ minute: start, delta: item.points })
          events.push({ minute: end, delta: -item.points })
        }
      }
    }
  }

  if (events.length === 0) return basePoints

  // Sort by minute ascending; if same minute, interval endings (-delta) before interval starts (+delta)
  events.sort((a, b) => {
    if (a.minute !== b.minute) return a.minute - b.minute
    return a.delta - b.delta
  })

  let current = basePoints
  let peak = basePoints

  for (const ev of events) {
    current += ev.delta
    if (current > peak) {
      peak = current
    }
  }

  return peak
}
