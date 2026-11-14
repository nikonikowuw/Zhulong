import { type RecordsTab } from '../data/schema'
import { type OverlayBurnOptions } from './canvas-overlay-drawer'
import { type CameraNameResolver } from './export-csv'
import { type ExportDraftOptions } from './export-db'

/**
 * 单次导出允许拉取的记录总量。
 */
export const DEFAULT_MAX_RECORDS = 20_000

/**
 * 单次导出允许打包的图片张数硬上限。
 */
export const HARD_MAX_IMAGES = 1_000

export type ExportPhase =
  | 'preparing'
  | 'fetching_records'
  | 'generating_csv'
  | 'processing_images'
  | 'zipping'
  | 'completed'

export interface ExportProgress {
  phase: ExportPhase
  current: number
  total: number
  percent?: number
  message?: string
}

export interface OverlayLabelContext {
  tab: RecordsTab
  personName?: string
  matched?: boolean
  similarity?: number
  alarmType?: string
  targetType?: string
  score?: number
}

export type OverlayLabelBuilder = (ctx: OverlayLabelContext) => string

export interface ExportProgressLabels {
  preparing?: string
  fetching?: (tab: RecordsTab, current: number, total: number) => string
  generatingCsv?: (tab: RecordsTab) => string
  generatingCsvSingle?: string
  processingImages?: (current: number, total: number) => string
  zipping?: string
  completed?: string
}

export interface ExportLabels {
  scene?: OverlayLabelBuilder
  person?: string
  progress?: ExportProgressLabels
}

export interface ExportQueryConfig {
  tabs: RecordsTab[]
  cameraId?: string
  startTime?: string
  endTime?: string
  matched?: boolean
  personId?: string
  alarmType?: string
  targetType?: string
}

export interface ExportOptions {
  selectedRecords?: {
    tab: RecordsTab
    items: import('../data/schema').RecordItem[]
  }
  queryConfig?: ExportQueryConfig
  exportCsv?: boolean
  exportImages?: boolean
  burnOverlay?: boolean
  imageKind?: 'scene' | 'closeup' | 'both'
  maxImages?: number
  maxRecords?: number
  resumeTaskId?: string
  signal?: AbortSignal
  resolveCameraName?: CameraNameResolver
  labels?: ExportLabels
  onProgress?: (progress: ExportProgress) => void
}

export interface ExportResult {
  blob: Blob
  filename: string
  failedImages: number
  truncatedImages: number
}

export class ExportLimitError extends Error {
  readonly scope: 'records' | 'images'
  readonly limit: number
  readonly actual: number

  constructor(scope: 'records' | 'images', limit: number, actual: number) {
    super(`Export limit exceeded: ${scope} ${actual} > ${limit}`)
    this.name = 'ExportLimitError'
    this.scope = scope
    this.limit = limit
    this.actual = actual
  }
}

export interface ImageJob {
  tab: RecordsTab
  recordId: string
  capturedAt: string
  kind: 'scene' | 'closeup'
  imagePath: string
  overlayOptions: OverlayBurnOptions
}

export interface JobFailure {
  tab: RecordsTab
  recordId: string
  kind: 'scene' | 'closeup'
  artifact: string
  reason: string
}

export interface ExportManifest {
  generator: 'zhulong-web'
  taskId: string
  mode: 'selection' | 'batch'
  createdAt: string
  exportedAt: string
  tabs: RecordsTab[]
  recordCount: number
  options: ExportDraftOptions
  images: {
    jobs: number
    exported: number
    failed: number
    truncated: number
  }
  reports: string[]
  failures: JobFailure[]
  warnings: string[]
}
