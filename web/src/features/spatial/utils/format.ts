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
