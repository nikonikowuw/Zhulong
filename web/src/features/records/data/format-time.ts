import { format } from 'date-fns'

/**
 * 安全格式化记录时间戳。
 *
 * 非法或缺失的时间串直接返回 `'-'`，避免 `date-fns/format`
 * 因 `Invalid Date` 抛出 `RangeError: Invalid time value` 打断整页渲染。
 */
export function formatRecordTime(
  value: string | undefined,
  pattern = 'yyyy-MM-dd HH:mm:ss'
): string {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return format(date, pattern)
}
