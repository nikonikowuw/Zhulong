import { format } from 'date-fns'
import { cameraLabel, type CameraNameLookup } from '../data/camera-label'
import {
  type Alarm,
  type Capture,
  type Recognition,
  type RecordItem,
  type RecordsTab,
} from '../data/schema'

/**
 * UTF-8 BOM 字符，用于解决 Windows Excel 打开 CSV 时的中文乱码问题。
 */
export const UTF8_BOM = '\uFEFF'

/**
 * Excel / WPS 会把以这些字符开头的单元格当公式求值（CSV 注入）。
 * 人员姓名、摄像机名、`attributes` 都可能来自外部上报，必须防御。
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

export type CameraNameResolver = CameraNameLookup

/**
 * 抓拍时间 → 浏览器本地时区（与详情弹窗、证据图文件名同源）。
 *
 * 规范要求「接口传输 UTC、展示用本地时区」。CSV 是交付给人的报表，
 * 属于展示侧；如果只写 `toISOString()`，国内部署下每一行都比页面上
 * 早 8 小时，对账时一定会被当成 bug 报回来。
 */
function formatLocalTime(isoStr?: string): string {
  if (!isoStr) return ''
  const d = new Date(isoStr)
  if (Number.isNaN(d.getTime())) return isoStr
  return format(d, 'yyyy-MM-dd HH:mm:ss')
}

/**
 * 抓拍时间 → ISO8601 UTC，供业务系统机器解析。
 */
function formatUtcTime(isoStr?: string): string {
  if (!isoStr) return ''
  const d = new Date(isoStr)
  if (Number.isNaN(d.getTime())) return isoStr
  return d.toISOString()
}

/** 时间列成对出现：本地可读 + UTC 可机读 */
function timeCells(isoStr?: string): [string, string] {
  return [formatLocalTime(isoStr), formatUtcTime(isoStr)]
}

const TIME_HEADERS = [
  '抓拍时间 (Captured At, Local)',
  '抓拍时间 (Captured At, UTC ISO8601)',
]

/**
 * 导出识别记录 CSV
 */
export function exportRecognitionsCsv(
  items: Recognition[],
  resolveCameraName?: CameraNameResolver
): string {
  const headers = [
    'ID',
    ...TIME_HEADERS,
    '摄像机名称 (Camera Name)',
    '摄像机 ID (Camera ID)',
    '算法名称 (Algorithm Name)',
    '算法版本 (Algorithm Version)',
    '是否命中 (Matched)',
    '比对相似度 (Similarity)',
    '人员姓名 (Person Name)',
    '外部人员 ID (External Person ID)',
    '内部人员 ID (Internal Person ID)',
    '跟踪 ID (Track ID)',
    'PTS 帧时间戳',
    '目标框 (Bounding Box)',
    '人体框 (Body Box)',
    '全景大图路径 (Image Path)',
    '人脸特写路径 (Face Image Path)',
    '底库图片路径 (Gallery Image Path)',
  ]

  const rows = items.map((r) => [
    r.id,
    ...timeCells(r.capturedAt),
    cameraLabel(r.cameraId, resolveCameraName),
    r.cameraId,
    r.algorithmName || r.algorithmId || '',
    r.algorithmVersion || '',
    r.matched ? '是' : '否',
    r.similarity != null ? Number(r.similarity).toFixed(4) : '',
    r.personName || '',
    r.personExternalId || '',
    r.personId || '',
    r.trackId,
    r.pts,
    r.box ? JSON.stringify(r.box) : '',
    r.bodyBox ? JSON.stringify(r.bodyBox) : '',
    r.imagePath || '',
    r.faceImagePath || '',
    r.galleryImagePath || '',
  ])

  return toCsv(headers, rows)
}

/**
 * 导出安全告警记录 CSV
 */
export function exportAlarmsCsv(
  items: Alarm[],
  resolveCameraName?: CameraNameResolver
): string {
  const headers = [
    'ID',
    ...TIME_HEADERS,
    '摄像机名称 (Camera Name)',
    '摄像机 ID (Camera ID)',
    '算法名称 (Algorithm Name)',
    '算法版本 (Algorithm Version)',
    '告警类型 (Alarm Type)',
    '告警得分 (Score)',
    'PTS 帧时间戳',
    '目标框 (Bounding Box)',
    '全景大图路径 (Image Path)',
    '特写抠图路径 (Crop Path)',
    '扩展信息 (Extra)',
  ]

  const rows = items.map((a) => [
    a.id,
    ...timeCells(a.capturedAt),
    cameraLabel(a.cameraId, resolveCameraName),
    a.cameraId,
    a.algorithmName || a.algorithmId || '',
    a.algorithmVersion || '',
    a.alarmType || '',
    a.score != null ? Number(a.score).toFixed(4) : '',
    a.pts,
    a.box ? JSON.stringify(a.box) : '',
    a.imagePath || '',
    a.cropPath || '',
    a.extra ? JSON.stringify(a.extra) : '',
  ])

  return toCsv(headers, rows)
}

/**
 * 导出目标/车辆抓拍记录 CSV
 */
export function exportCapturesCsv(
  items: Capture[],
  resolveCameraName?: CameraNameResolver
): string {
  const headers = [
    'ID',
    ...TIME_HEADERS,
    '摄像机名称 (Camera Name)',
    '摄像机 ID (Camera ID)',
    '算法名称 (Algorithm Name)',
    '算法版本 (Algorithm Version)',
    '目标类型 (Target Type)',
    '置信度得分 (Score)',
    '跟踪 ID (Track ID)',
    'PTS 帧时间戳',
    '目标框 (Bounding Box)',
    '全景大图路径 (Image Path)',
    '特写抠图路径 (Crop Path)',
    '属性信息 (Attributes)',
  ]

  const rows = items.map((c) => [
    c.id,
    ...timeCells(c.capturedAt),
    cameraLabel(c.cameraId, resolveCameraName),
    c.cameraId,
    c.algorithmName || c.algorithmId || '',
    c.algorithmVersion || '',
    c.targetType || '',
    c.score != null ? Number(c.score).toFixed(4) : '',
    c.trackId,
    c.pts,
    c.box ? JSON.stringify(c.box) : '',
    c.imagePath || '',
    c.cropPath || '',
    c.attributes ? JSON.stringify(c.attributes) : '',
  ])

  return toCsv(headers, rows)
}

function toCsv(headers: string[], rows: unknown[][]): string {
  const csvContent = [
    headers.map(escapeCsvField).join(','),
    ...rows.map((row) => row.map(escapeCsvField).join(',')),
  ].join('\r\n')

  return `${UTF8_BOM}${csvContent}`
}

/**
 * 统一根据 Tab 类型导出对应 CSV
 */
export function exportRecordsToCsv(
  tab: RecordsTab,
  records: RecordItem[],
  resolveCameraName?: CameraNameResolver
): string {
  switch (tab) {
    case 'recognitions':
      return exportRecognitionsCsv(records as Recognition[], resolveCameraName)
    case 'alarms':
      return exportAlarmsCsv(records as Alarm[], resolveCameraName)
    case 'captures':
    case 'vehicles':
      return exportCapturesCsv(records as Capture[], resolveCameraName)
  }
}
