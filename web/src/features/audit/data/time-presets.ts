import { endOfDay, startOfDay, subDays } from 'date-fns'

export type AuditDateRange = {
  preset?: TimePresetKey
  startTime?: string
  endTime?: string
}

export type TimePresetKey =
  | '15m'
  | '30m'
  | '1h'
  | '6h'
  | '24h'
  | 'today'
  | '7d'
  | '30d'

export function computeRangeFromPreset(
  preset: TimePresetKey,
  referenceDate = new Date()
): { startTime: string; endTime: string } {
  const now = referenceDate
  let start: Date
  let end = now

  switch (preset) {
    case '15m':
      start = new Date(now.getTime() - 15 * 60 * 1000)
      break
    case '30m':
      start = new Date(now.getTime() - 30 * 60 * 1000)
      break
    case '1h':
      start = new Date(now.getTime() - 60 * 60 * 1000)
      break
    case '6h':
      start = new Date(now.getTime() - 6 * 60 * 60 * 1000)
      break
    case '24h':
      start = new Date(now.getTime() - 24 * 60 * 60 * 1000)
      break
    case 'today':
      start = startOfDay(now)
      end = endOfDay(now)
      break
    case '7d':
      start = startOfDay(subDays(now, 6))
      end = endOfDay(now)
      break
    case '30d':
      start = startOfDay(subDays(now, 29))
      end = endOfDay(now)
      break
  }

  return {
    startTime: start.toISOString(),
    endTime: end.toISOString(),
  }
}
