import { endOfDay, startOfDay, subDays } from 'date-fns'

export type RecordsDateRange = {
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

/**
 * 感知记录页的默认时间窗口：当天。
 *
 * 感知记录是持续写入的，不带时间下限的默认查询会让「今天发生的」淹没在历史里，
 * 首屏还要让 SQLite 从最新一条往回扫。默认收窄到自然日，历史数据仍然可以通过
 * 日历自定义区间查看。
 */
export const DEFAULT_TIME_PRESET: TimePresetKey = 'today'

export const RECENT_TIME_PRESETS: { key: TimePresetKey; labelKey: string }[] = [
  { key: '15m', labelKey: 'toolbar.presets.15m' },
  { key: '30m', labelKey: 'toolbar.presets.30m' },
  { key: '1h', labelKey: 'toolbar.presets.1h' },
  { key: '6h', labelKey: 'toolbar.presets.6h' },
  { key: '24h', labelKey: 'toolbar.presets.24h' },
]

export const PERIOD_TIME_PRESETS: { key: TimePresetKey; labelKey: string }[] = [
  { key: 'today', labelKey: 'toolbar.presets.today' },
  { key: '7d', labelKey: 'toolbar.presets.7d' },
  { key: '30d', labelKey: 'toolbar.presets.30d' },
]

export type ResolvedRecordsWindow = {
  /** 用于展示与高亮的预设；自定义区间时为 undefined */
  preset?: TimePresetKey
  startTime?: string
  endTime?: string
  /** true 表示当前窗口就是默认的「今日全天」，界面上不算用户主动筛选 */
  isDefaultWindow: boolean
}

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

/**
 * 把 URL 状态解析成真正生效的查询窗口。
 *
 * - URL 里有绝对时间（日历自定义区间）→ 以绝对时间为准，预设标签让位给时间标签。
 *   这也覆盖了「preset 与时间同时存在」的旧链接：标签与数据不会再互相矛盾。
 * - 只有预设 → 以当前时刻现算。URL 只记意图、不存时间戳，跨天刷新不会把「今日全天」
 *   钉在昨天的窗口上。
 * - 什么都没有 → 默认今日全天。
 *
 * 相对预设依赖 `new Date()`，调用方必须 memo（依赖 preset/startTime/endTime）后再
 * 交给 react-query，否则每次渲染都会生成新的 query key。
 */
export function resolveRecordsWindow(
  range: RecordsDateRange,
  referenceDate = new Date()
): ResolvedRecordsWindow {
  if (range.startTime || range.endTime) {
    return {
      startTime: range.startTime,
      endTime: range.endTime,
      isDefaultWindow: false,
    }
  }

  const preset = range.preset ?? DEFAULT_TIME_PRESET
  const { startTime, endTime } = computeRangeFromPreset(preset, referenceDate)
  return {
    preset,
    startTime,
    endTime,
    isDefaultWindow: preset === DEFAULT_TIME_PRESET,
  }
}
