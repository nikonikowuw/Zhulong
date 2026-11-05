import {
  statusDotClass,
  statusTextClass,
  type StatusLevel,
} from '@/lib/status-palette'

export function formatBytes(bytes: number, decimals = 1): string {
  if (!bytes || bytes <= 0) return '0 B'
  const k = 1024
  const dm = decimals < 0 ? 0 : decimals
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  const idx = Math.min(i, sizes.length - 1)
  return `${parseFloat((bytes / Math.pow(k, idx)).toFixed(dm))} ${sizes[idx]}`
}

export function formatRate(bytesPerSec: number): string {
  if (!bytesPerSec || bytesPerSec <= 0) return '0 B/s'
  return `${formatBytes(bytesPerSec)}/s`
}

export function formatUptime(seconds: number): string {
  if (!seconds || seconds <= 0) return '0m'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)

  const parts: string[] = []
  if (days > 0) parts.push(`${days}d`)
  if (hours > 0 || days > 0) parts.push(`${hours}h`)
  parts.push(`${minutes}m`)
  return parts.join(' ')
}

/**
 * 资源占用率 → 四级状态语义（§1.3.3）。阈值显式声明，不依赖任何数组下标。
 *
 * 分类语义与状态语义的边界：占用率表达「现在健康不健康」，属于状态，
 * 因此对外只暴露 `StatusLevel`，具体色串一律由 `@/lib/status-palette` 查表提供。
 */
export function usageLevel(percent: number): StatusLevel {
  if (percent >= 90) return 'destructive'
  if (percent >= 80) return 'warning'
  return 'active'
}

/**
 * 占用率数值的文字色。
 *
 * 正常档回落 `text-foreground` 而非 `active` 的 teal：仪表盘上五个大数字同时染绿
 * 会把「品牌强调色」稀释成噪音，只有进入警戒/危险档才值得用颜色喊话。
 */
export function getUsageStatusColor(percent: number): string {
  const level = usageLevel(percent)
  return level === 'active' ? 'text-foreground' : statusTextClass(level)
}

/**
 * 占用率进度条的填充色。
 *
 * 正常档用品牌主色 `bg-primary`（与全局行动点同色，克制且成体系），
 * 警戒/危险档才切换到状态色。
 */
export function getProgressIndicatorClass(percent: number): string {
  const level = usageLevel(percent)
  return level === 'active' ? 'bg-primary' : statusDotClass(level)
}

/** 温度 → 四级状态语义：≥75°C 危险，≥60°C 警戒。 */
export function thermalLevel(temperature: number): StatusLevel {
  if (temperature >= 75) return 'destructive'
  if (temperature >= 60) return 'warning'
  return 'active'
}

/** 温度数值的文字色，正常档回落前景色（同 `getUsageStatusColor` 的理由）。 */
export function getThermalTextColor(temperature: number): string {
  const level = thermalLevel(temperature)
  return level === 'active' ? 'text-foreground' : statusTextClass(level)
}

/** 温度微型标尺的填充色。 */
export function getThermalBarClass(temperature: number): string {
  const level = thermalLevel(temperature)
  return level === 'active' ? 'bg-primary' : statusDotClass(level)
}
