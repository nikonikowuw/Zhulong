import { type StatusLevel } from '@/lib/status-palette'

/** 时间戳缺失时的占位符，避免在表格里渲染 `Invalid Date`。 */
export const EMPTY_PLACEHOLDER = '—'

/**
 * 格式化后端返回的 RFC 3339 时间戳。
 *
 * 无法解析时原样返回：宁可显示原始字符串，也不要把设备的注册时间
 * 替换成一个看起来正常的错误时间。
 */
export function formatTimestamp(
  value: string | null | undefined,
  language: string
): string {
  if (!value) return EMPTY_PLACEHOLDER
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat(language, {
    dateStyle: 'short',
    timeStyle: 'medium',
  }).format(date)
}

/**
 * 运行状态 → 语义色级。
 *
 * `disabled` 是正常态而不是故障：未启用时不应该显示成红色。
 */
export function runtimeStateLevel(state: string): StatusLevel {
  switch (state) {
    case 'running':
      return 'active'
    case 'error':
      return 'destructive'
    case 'disabled':
    default:
      return 'inactive'
  }
}

/** 设备在线状态 → 语义色级（离线按规范归入破坏性档）。 */
export function deviceStateLevel(state: string): StatusLevel {
  switch (state) {
    case 'online':
      return 'active'
    case 'offline':
      return 'destructive'
    default:
      return 'inactive'
  }
}
