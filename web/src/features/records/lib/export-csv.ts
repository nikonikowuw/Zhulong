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
 * 根据 RFC 4180 标准转义 CSV 单元格内容
 */
export function escapeCsvField(value: unknown): string {
  if (value === null || value === undefined) {
    return ''
  }
  const str = typeof value === 'object' ? JSON.stringify(value) : String(value)
  if (
    str.includes(',') ||
    str.includes('"') ||
    str.includes('\n') ||
    str.includes('\r')
  ) {
    return `"${str.replace(/"/g, '""')}"`
  }
  return str
}

export type CameraNameResolver = CameraNameLookup

/**
 * 格式化通用时间字符串（保持 ISO8601 或标准展示）
 */
function formatTime(isoStr?: string): string {
  if (!isoStr) return ''
  try {
    const d = new Date(isoStr)
    if (isNaN(d.getTime())) return isoStr
    return d.toISOString()
  } catch {
    return isoStr
  }
}

/**
 * 导出识别记录 CSV
 */
export function exportRecognitionsCsv(
  items: Recognition[],
  resolveCameraName?: CameraNameResolver
): string {
  const headers = [
    'ID',
    '抓拍时间 (Captured At)',
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
    formatTime(r.capturedAt),
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

  const csvContent = [
    headers.map(escapeCsvField).join(','),
    ...rows.map((row) => row.map(escapeCsvField).join(',')),
  ].join('\r\n')

  return `${UTF8_BOM}${csvContent}`
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
    '告警时间 (Captured At)',
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
    formatTime(a.capturedAt),
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

  const csvContent = [
    headers.map(escapeCsvField).join(','),
    ...rows.map((row) => row.map(escapeCsvField).join(',')),
  ].join('\r\n')

  return `${UTF8_BOM}${csvContent}`
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
    '抓拍时间 (Captured At)',
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
    formatTime(c.capturedAt),
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
