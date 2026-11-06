import { format } from 'date-fns'

/**
 * UTF-8 BOM 字符，用于解决 Windows Excel 打开 CSV 时的中文乱码问题。
 */
export const UTF8_BOM = '\uFEFF'

/**
 * Excel / WPS 会把以这些字符开头的单元格当公式求值（CSV 注入）。
 * 人员姓名、摄像机名、`attributes`、审计 `detail` 都可能来自外部上报，必须防御。
 */
const FORMULA_PREFIX = /^[=+\-@\t\r]/

/**
 * 根据 RFC 4180 标准转义 CSV 单元格内容，并阻断公式注入。
 *
 * 数值类型不做前缀防护：`-0.5` 只是负数，Excel 不会把它当公式；
 * 加了 `'` 反而会把数字变成文本，破坏业务系统对账。
 */
export function escapeCsvField(value: unknown): string {
  if (value === null || value === undefined) {
    return ''
  }
  const raw = typeof value === 'object' ? JSON.stringify(value) : String(value)
  const guarded =
    typeof value !== 'number' && FORMULA_PREFIX.test(raw) ? `'${raw}` : raw
  if (
    guarded.includes(',') ||
    guarded.includes('"') ||
    guarded.includes('\n') ||
    guarded.includes('\r')
  ) {
    return `"${guarded.replace(/"/g, '""')}"`
  }
  return guarded
}

/**
 * 组装带 BOM 的 CSV 文本：首行表头，`\r\n` 分行（RFC 4180）。
 */
export function toCsv(
  headers: readonly unknown[],
  rows: readonly (readonly unknown[])[]
): string {
  const lines = [headers, ...rows].map((row) =>
    row.map(escapeCsvField).join(',')
  )
  return `${UTF8_BOM}${lines.join('\r\n')}`
}

/**
 * RFC3339 UTC → 浏览器本地时区（`yyyy-MM-dd HH:mm:ss`）。
 *
 * 规范要求「接口传输 UTC、展示用本地时区」。CSV 是交付给人的报表，属于展示侧；
 * 只写 `toISOString()` 会让国内部署下每一行都比页面上早 8 小时，对账时会被当成 bug。
 */
export function formatLocalDateTime(isoStr: string): string {
  if (!isoStr) return ''
  const d = new Date(isoStr)
  if (Number.isNaN(d.getTime())) return isoStr
  return format(d, 'yyyy-MM-dd HH:mm:ss')
}

/**
 * 触发浏览器下载。
 *
 * 撤销太早会让部分浏览器（Safari/Firefox 对大 Blob 尤其明显）在下载真正启动前
 * 就把 URL 吊销掉，留下一个失败项，因此延迟 10s 再 revoke。
 */
export function triggerDownload(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  setTimeout(() => URL.revokeObjectURL(url), 10_000)
}
