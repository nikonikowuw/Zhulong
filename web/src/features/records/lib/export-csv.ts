import { formatLocalDateTime, toCsv } from '@/lib/csv'
import i18n from '@/lib/i18n'
import { cameraLabel, type CameraNameLookup } from '../data/camera-label'
import {
  type Alarm,
  type Capture,
  type Recognition,
  type RecordItem,
  type RecordsTab,
} from '../data/schema'

export type CameraNameResolver = CameraNameLookup

/**
 * 抓拍时间 → 浏览器本地时区（与详情弹窗、证据图文件名同源）。
 *
 * 规范要求「接口传输 UTC、展示用本地时区」。CSV 是交付给人的报表，
 * 属于展示侧；如果只写 `toISOString()`，国内部署下每一行都比页面上
 * 早 8 小时，对账时一定会被当成 bug 报回来。
 *
 * 格式化实现收敛在 `@/lib/csv`，audit 导出与这里共用同一份时区口径。
 */
function formatLocalTime(isoStr?: string): string {
  return isoStr ? formatLocalDateTime(isoStr) : ''
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

function csvHeader(key: string): string {
  return i18n.t(`records:export.csv.${key}`)
}

function timeHeaders(): [string, string] {
  return [csvHeader('capturedAtLocal'), csvHeader('capturedAtUtc')]
}

/**
 * 导出识别记录 CSV
 */
export function exportRecognitionsCsv(
  items: Recognition[],
  resolveCameraName?: CameraNameResolver
): string {
  const headers = [
    csvHeader('id'),
    ...timeHeaders(),
    csvHeader('cameraName'),
    csvHeader('cameraId'),
    csvHeader('algorithmName'),
    csvHeader('algorithmVersion'),
    csvHeader('matched'),
    csvHeader('similarity'),
    csvHeader('personName'),
    csvHeader('externalPersonId'),
    csvHeader('internalPersonId'),
    csvHeader('trackId'),
    csvHeader('pts'),
    csvHeader('boundingBox'),
    csvHeader('bodyBox'),
    csvHeader('imagePath'),
    csvHeader('faceImagePath'),
    csvHeader('galleryImagePath'),
  ]

  const rows = items.map((r) => [
    r.id,
    ...timeCells(r.capturedAt),
    cameraLabel(r.cameraId, resolveCameraName),
    r.cameraId,
    r.algorithmName || r.algorithmId || '',
    r.algorithmVersion || '',
    r.matched ? csvHeader('yes') : csvHeader('no'),
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
    csvHeader('id'),
    ...timeHeaders(),
    csvHeader('cameraName'),
    csvHeader('cameraId'),
    csvHeader('algorithmName'),
    csvHeader('algorithmVersion'),
    csvHeader('alarmType'),
    csvHeader('score'),
    csvHeader('pts'),
    csvHeader('boundingBox'),
    csvHeader('imagePath'),
    csvHeader('cropPath'),
    csvHeader('extra'),
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
    csvHeader('id'),
    ...timeHeaders(),
    csvHeader('cameraName'),
    csvHeader('cameraId'),
    csvHeader('algorithmName'),
    csvHeader('algorithmVersion'),
    csvHeader('targetType'),
    csvHeader('confidenceScore'),
    csvHeader('trackId'),
    csvHeader('pts'),
    csvHeader('boundingBox'),
    csvHeader('imagePath'),
    csvHeader('cropPath'),
    csvHeader('attributes'),
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
