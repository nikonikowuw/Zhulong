import { type TimeRangePreset } from '../data/schema'

/**
 * 把 RFC3339 时间戳格式化成容器本地时钟（时:分:秒）。
 *
 * @returns 格式化后的时钟文本；缺值或无法解析时返回空串，调用方据此隐藏该行。
 */
export function formatClock(value?: string): string {
  if (!value) {
    return ''
  }
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) {
    return ''
  }
  return parsed.toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

/** 时间预设 → 绝对区间（浏览器本地时区，发送前统一转 UTC RFC3339）。 */
export function resolveTimeRange(preset: TimeRangePreset): {
  startTime: string
  endTime: string
} {
  const now = new Date()
  let start = new Date()

  switch (preset) {
    case '1h':
      start = new Date(now.getTime() - 60 * 60 * 1000)
      break
    case 'today':
      start = new Date(
        now.getFullYear(),
        now.getMonth(),
        now.getDate(),
        0,
        0,
        0
      )
      break
    case '24h':
      start = new Date(now.getTime() - 24 * 60 * 60 * 1000)
      break
    case '7d':
      start = new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000)
      break
  }

  return {
    startTime: start.toISOString(),
    endTime: now.toISOString(),
  }
}
