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
