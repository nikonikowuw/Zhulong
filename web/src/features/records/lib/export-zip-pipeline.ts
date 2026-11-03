import { z } from 'zod'
import { format } from 'date-fns'
import { strFromU8, strToU8, zip, type AsyncZippable } from 'fflate'
import { recordsApi } from '../api/records-api'
import { evidenceFileName } from '../data/evidence-file-name'
import {
  boxSchema,
  recordsTabSchema,
  type Alarm,
  type Capture,
  type Recognition,
  type RecordItem,
  type RecordsTab,
} from '../data/schema'
import {
  burnOverlayOnImage,
  fetchImageAsBlob,
  MediaFetchError,
  type OverlayBurnOptions,
} from './canvas-overlay-drawer'
import { type CameraNameResolver, exportRecordsToCsv } from './export-csv'
import {
  exportDb,
  type ExportDraftOptions,
  type ExportTaskDraft,
} from './export-db'

/**
 * 单次导出允许拉取的记录总量。
 *
 * 超出就明确失败而不是静默把边缘机拖进 swap：批量模式会跨 4 个维度翻页，
 * 每页 100 条串行拉取，没有任何上限时一个「近 30 天 + 全部摄像头」的筛选
 * 就足以把标签页打爆。
 */
export const DEFAULT_MAX_RECORDS = 20_000

/**
 * 单次导出允许打包的图片张数硬上限。
 *
 * 用户取消勾选「限制最大导出图片数」时仍然生效 —— 否则 `zipFiles` + IndexedDB
 * + 最终 Blob 的三份副本会直接把内存吃光。截断会在 manifest 里显式记录。
 */
export const HARD_MAX_IMAGES = 1_000

const RECORD_PAGE_SIZE = 100
const IMAGE_CONCURRENCY = 3
/** 报表快照的产物键前缀；续传时靠它把 CSV 与图片锁在同一份数据集上 */
const REPORT_KEY_PREFIX = '__reports__/'
const REPORT_KEY_SUFFIX = '_records.csv'
const JOBS_ARTIFACT_KEY = '__jobs__.json'
const MANIFEST_KEY = 'manifest.json'
/** 每 N 张图 / 每 N 毫秒落一次草稿，避免 O(图数 × 记录数) 的克隆写放大 */
const DRAFT_PERSIST_EVERY = 10
const DRAFT_PERSIST_INTERVAL_MS = 1_000

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
  /**
   * 0..100 的跨阶段单调进度。UI 应优先使用它 —— `current/total` 是阶段内
   * 语义，多维度批量导出时每个维度都会从 0 重算，百分比会来回跳。
   */
  percent?: number
  message?: string
}

/** 烧录标签的取值上下文 */
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

/**
 * 烧录进图片的标签文案。
 *
 * 证据图是给人看的，标签必须跟随界面语言。这里传纯函数而不是持久化字段 ——
 * 函数只在 `extractImageJobs` 里被求值成字符串，落盘的是结果本身，
 * 所以不会撞上 IndexedDB 的 DataCloneError。
 */
export interface ExportLabels {
  /** 主框（全景）标签 */
  scene?: OverlayLabelBuilder
  /** 人体框标签，默认 `Person` */
  person?: string
}

/** 批量模式下的查询条件 */
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
  /** 模式一：勾选导出（严格限定在当前选中的 Tab 与记录） */
  selectedRecords?: {
    tab: RecordsTab
    items: RecordItem[]
  }
  /** 模式二：全局条件导出（支持多 Tab 组合） */
  queryConfig?: ExportQueryConfig
  /** 导出 CSV 开关，默认 true */
  exportCsv?: boolean
  /** 导出图片开关，默认 false */
  exportImages?: boolean
  /** 是否在图片上烧录检测框与研判标签，默认 true */
  burnOverlay?: boolean
  /** 图片导出类型: 'scene' (全景), 'closeup' (特写), 'both' (两者)，默认 'scene' */
  imageKind?: 'scene' | 'closeup' | 'both'
  /** 最大导出图片张数；缺省仍受 HARD_MAX_IMAGES 保护 */
  maxImages?: number
  /** 单次导出的记录条数上限，默认 DEFAULT_MAX_RECORDS */
  maxRecords?: number
  /** 断点续传任务 ID */
  resumeTaskId?: string
  /** 取消信号 */
  signal?: AbortSignal
  /** 摄像机名称解析函数 */
  resolveCameraName?: CameraNameResolver
  /** 烧录标签文案（i18n） */
  labels?: ExportLabels
  /** 实时进度回调 */
  onProgress?: (progress: ExportProgress) => void
}

export interface ExportResult {
  blob: Blob
  filename: string
  /** 打包时被跳过的失败图片数（明细写在 manifest.json 里） */
  failedImages: number
  /** 因图片上限被截断的任务数 */
  truncatedImages: number
}

/** 超出预算时抛出，调用方应展示「缩小范围」而不是通用失败文案 */
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

interface JobFailure {
  tab: RecordsTab
  recordId: string
  kind: 'scene' | 'closeup'
  artifact: string
  reason: string
}

interface ExportManifest {
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
/** 落盘的图片任务清单（JSON，全部是可结构化克隆的原始值） */
const overlayOptionsSchema = z.object({
  primaryBox: boxSchema.optional(),
  primaryLabel: z.string().optional(),
  secondaryBox: boxSchema.optional(),
  secondaryLabel: z.string().optional(),
  palette: z.enum(['emerald', 'rose', 'amber', 'sky', 'violet']).optional(),
  secondaryPalette: z
    .enum(['emerald', 'rose', 'amber', 'sky', 'violet'])
    .optional(),
})

const imageJobSchema = z.object({
  tab: recordsTabSchema,
  recordId: z.string(),
  capturedAt: z.string().default(''),
  kind: z.enum(['scene', 'closeup']),
  imagePath: z.string(),
  overlayOptions: overlayOptionsSchema.default({}),
})

const jobsSnapshotSchema = z.object({
  version: z.literal(1),
  tabs: z.array(recordsTabSchema).min(1),
  /** 因图片上限被截断的任务数，续传时要一并还原到 manifest */
  truncated: z.number().int().nonnegative().default(0),
  jobs: z.array(imageJobSchema),
})

type JobsSnapshot = z.infer<typeof jobsSnapshotSchema>

interface ResumeSnapshot {
  tabs: RecordsTab[]
  jobs: ImageJob[]
  truncated: number
  reports: Map<RecordsTab, Uint8Array>
}

const PHASE_RANGE: Record<ExportPhase, [number, number]> = {
  preparing: [0, 2],
  fetching_records: [2, 38],
  generating_csv: [38, 46],
  processing_images: [46, 92],
  zipping: [92, 99],
  completed: [99, 100],
}

type Emit = (
  phase: ExportPhase,
  current: number,
  total: number,
  message: string | undefined,
  ratio: number
) => void

function percentFor(phase: ExportPhase, ratio: number): number {
  const [start, end] = PHASE_RANGE[phase]
  const safe = Number.isFinite(ratio) ? ratio : 0
  const clamped = Math.min(1, Math.max(0, safe))
  return Math.round(start + (end - start) * clamped)
}

function abortError(): DOMException {
  return new DOMException('Export cancelled', 'AbortError')
}

function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === 'AbortError'
}

function assertNotAborted(signal?: AbortSignal): void {
  if (signal?.aborted) throw abortError()
}

function delay(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(abortError())
      return
    }
    const onAbort = () => {
      clearTimeout(timer)
      reject(abortError())
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort)
      resolve()
    }, ms)
    signal?.addEventListener('abort', onAbort, { once: true })
  })
}

/**
 * 是否值得重试。
 *
 * 404 表示快照已经不在了 —— 再试三次只会把取消/失败窗口拖长，
 * 而取消失败还会被当成「单图失败」吞掉。
 */
function isRetryable(err: unknown): boolean {
  if (isAbortError(err)) return false
  if (err instanceof MediaFetchError) {
    return err.status >= 500 || err.status === 408 || err.status === 429
  }
  return true
}

/** 单操作网络指数退避重试执行器（可取消、不重试确定性失败） */
async function retryAsync<T>(
  fn: () => Promise<T>,
  signal?: AbortSignal,
  retries = 3,
  delayMs = 300
): Promise<T> {
  let lastError: unknown
  for (let attempt = 0; attempt < retries; attempt++) {
    assertNotAborted(signal)
    try {
      return await fn()
    } catch (err) {
      lastError = err
      if (!isRetryable(err) || attempt === retries - 1) throw err
      await delay(delayMs * 2 ** attempt, signal)
    }
  }
  throw lastError
}

function describeError(err: unknown): string {
  if (err instanceof Error) return err.message
  return String(err)
}

function localStamp(date = new Date()): string {
  return format(date, 'yyyyMMdd_HHmmss')
}

/** `crypto.randomUUID` 需要安全上下文；板子常见的是局域网 http，必须有兜底 */
function createTaskId(): string {
  const random =
    globalThis.crypto?.randomUUID?.() ?? Math.random().toString(36).slice(2, 10)
  return `task_${Date.now()}_${random}`
}

function reportKey(tab: RecordsTab): string {
  return `${REPORT_KEY_PREFIX}${tab}${REPORT_KEY_SUFFIX}`
}

function reportFileName(tab: RecordsTab): string {
  return `${tab}${REPORT_KEY_SUFFIX}`
}

/**
 * 触发浏览器动态下载。
 *
 * 撤销太早会让部分浏览器（Safari/Firefox 对大 Blob 尤其明显）在下载
 * 真正启动前就把 URL 吊销掉，留下一个失败项。
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

function defaultSceneLabel(ctx: OverlayLabelContext): string {
  const pct = ((ctx.similarity ?? ctx.score ?? 0) * 100).toFixed(0)
  if (ctx.tab === 'recognitions') {
    if (!ctx.matched) return `Stranger (${pct}%)`
    return ctx.personName ? `${ctx.personName} (${pct}%)` : `Matched (${pct}%)`
  }
  if (ctx.tab === 'alarms') return `${ctx.alarmType ?? ''} (${pct}%)`
  return `${ctx.targetType ?? ''} (${pct}%)`
}

/**
 * 提取记录中的图片导出任务。
 *
 * 标签在这里就求值成字符串：任务清单要落进 IndexedDB 供断点续传，
 * 存函数会在 `put()` 时抛 DataCloneError。
 */
function extractImageJobs(
  tab: RecordsTab,
  records: RecordItem[],
  imageKind: 'scene' | 'closeup' | 'both',
  burnOverlay: boolean,
  labels?: ExportLabels
): ImageJob[] {
  const jobs: ImageJob[] = []
  const sceneLabel = labels?.scene ?? defaultSceneLabel
  const personLabel = labels?.person ?? 'Person'
  const wantScene = imageKind === 'scene' || imageKind === 'both'
  const wantCloseup = imageKind === 'closeup' || imageKind === 'both'

  for (const item of records) {
    if (tab === 'recognitions') {
      const rec = item as Recognition
      const overlayOptions: OverlayBurnOptions = burnOverlay
        ? {
            primaryBox: rec.box,
            primaryLabel: sceneLabel({
              tab,
              personName: rec.personName,
              matched: rec.matched,
              similarity: rec.similarity,
            }),
            palette: rec.matched ? 'emerald' : 'rose',
            secondaryBox: rec.bodyBox,
            secondaryLabel: personLabel,
            secondaryPalette: 'violet',
          }
        : {}

      if (wantScene && rec.imagePath) {
        jobs.push({
          tab,
          recordId: rec.id,
          capturedAt: rec.capturedAt,
          kind: 'scene',
          imagePath: rec.imagePath,
          overlayOptions,
        })
      }
      if (wantCloseup && rec.faceImagePath) {
        // 特写截图通常无需再重复画全身框
        jobs.push({
          tab,
          recordId: rec.id,
          capturedAt: rec.capturedAt,
          kind: 'closeup',
          imagePath: rec.faceImagePath,
          overlayOptions: {},
        })
      }
      continue
    }

    if (tab === 'alarms') {
      const alarm = item as Alarm
      const overlayOptions: OverlayBurnOptions = burnOverlay
        ? {
            primaryBox: alarm.box,
            primaryLabel: sceneLabel({
              tab,
              alarmType: alarm.alarmType,
              score: alarm.score,
            }),
            palette: 'amber',
          }
        : {}

      if (wantScene && alarm.imagePath) {
        jobs.push({
          tab,
          recordId: alarm.id,
          capturedAt: alarm.capturedAt,
          kind: 'scene',
          imagePath: alarm.imagePath,
          overlayOptions,
        })
      }
      if (wantCloseup && alarm.cropPath) {
        jobs.push({
          tab,
          recordId: alarm.id,
          capturedAt: alarm.capturedAt,
          kind: 'closeup',
          imagePath: alarm.cropPath,
          overlayOptions: {},
        })
      }
      continue
    }

    // captures / vehicles
    const cap = item as Capture
    const overlayOptions: OverlayBurnOptions = burnOverlay
      ? {
          primaryBox: cap.box,
          primaryLabel: sceneLabel({
            tab,
            targetType: cap.targetType,
            score: cap.score,
          }),
          palette: 'sky',
        }
      : {}

    if (wantScene && cap.imagePath) {
      jobs.push({
        tab,
        recordId: cap.id,
        capturedAt: cap.capturedAt,
        kind: 'scene',
        imagePath: cap.imagePath,
        overlayOptions,
      })
    }
    if (wantCloseup && cap.cropPath) {
      jobs.push({
        tab,
        recordId: cap.id,
        capturedAt: cap.capturedAt,
        kind: 'closeup',
        imagePath: cap.cropPath,
        overlayOptions: {},
      })
    }
  }

  return jobs
}

/**
 * ZIP 内的图片路径。
 *
 * 复用详情页的 `evidenceFileName`：裸 ULID 从下载目录里捞出来时，
 * 唯一想知道的就是「这是什么时候拍的」，与单张下载保持同一套命名。
 */
function jobArtifactPath(job: ImageJob): string {
  const base = evidenceFileName(job.recordId, job.capturedAt).replace(
    /\.jpg$/i,
    ''
  )
  return `images/${job.tab}/${base}_${job.kind}.jpg`
}

/** 受控并发工作池执行器 */
async function runConcurrentPool<T>(
  items: T[],
  limit: number,
  signal: AbortSignal | undefined,
  fn: (item: T) => Promise<void>
): Promise<void> {
  let currentIndex = 0

  const workers = Array.from(
    { length: Math.min(limit, items.length) },
    async () => {
      while (currentIndex < items.length) {
        assertNotAborted(signal)
        const index = currentIndex++
        await fn(items[index])
      }
    }
  )

  await Promise.all(workers)
}

interface RecordPage {
  items: RecordItem[]
  total: number
}

/**
 * 单页记录请求。
 *
 * ⚠️ `vehicles` 维度的 `targetType='vehicle'` 只在这里决定 —— 调用方传进来的
 * `targetType` 表达的是「用户显式选择的目标类型筛选」，不是 Tab 语义。
 * 早前版本在页面层把 Tab 语义预编译进 `targetType`，导致从「车辆记录」Tab
 * 打开批量导出并勾选「抓拍记录」时，抓拍维度也被过滤成车辆。
 */
async function requestRecordPage(
  tab: RecordsTab,
  query: ExportQueryConfig,
  page: number,
  pageSize: number,
  endTime: string
): Promise<RecordPage> {
  if (tab === 'recognitions') {
    const resp = await recordsApi.getRecognitions({
      page,
      pageSize,
      cameraId: query.cameraId,
      personId: query.personId,
      matched: query.matched,
      startTime: query.startTime,
      endTime,
    })
    return { items: resp.items, total: resp.total }
  }
  if (tab === 'alarms') {
    const resp = await recordsApi.getAlarms({
      page,
      pageSize,
      cameraId: query.cameraId,
      alarmType: query.alarmType,
      startTime: query.startTime,
      endTime,
    })
    return { items: resp.items, total: resp.total }
  }
  const resp = await recordsApi.getCaptures({
    page,
    pageSize,
    cameraId: query.cameraId,
    targetType: tab === 'vehicles' ? 'vehicle' : query.targetType,
    startTime: query.startTime,
    endTime,
  })
  return { items: resp.items, total: resp.total }
}

/** 先探一次各维度的 total：既能守住记录预算，也能让进度单调 */
async function probeRecordTotals(
  query: ExportQueryConfig,
  tabs: RecordsTab[],
  endTime: string,
  signal: AbortSignal | undefined
): Promise<number> {
  let total = 0
  for (const tab of tabs) {
    assertNotAborted(signal)
    const page = await requestRecordPage(tab, query, 1, 1, endTime)
    total += page.total
  }
  return total
}

async function fetchRecordsByQuery(
  query: ExportQueryConfig,
  tabs: RecordsTab[],
  endTime: string,
  expectedTotal: number,
  maxRecords: number,
  signal: AbortSignal | undefined,
  emit: Emit
): Promise<Map<RecordsTab, RecordItem[]>> {
  const dataset = new Map<RecordsTab, RecordItem[]>()
  const denom = Math.max(expectedTotal, 1)
  let fetched = 0

  for (const tab of tabs) {
    const items: RecordItem[] = []
    let page = 1
    while (true) {
      assertNotAborted(signal)
      const resp = await requestRecordPage(
        tab,
        query,
        page,
        RECORD_PAGE_SIZE,
        endTime
      )
      items.push(...resp.items)
      fetched += resp.items.length

      if (fetched > maxRecords) {
        throw new ExportLimitError('records', maxRecords, fetched)
      }

      emit(
        'fetching_records',
        fetched,
        Math.max(expectedTotal, fetched),
        `Fetching ${tab}... (${fetched}/${Math.max(expectedTotal, fetched)})`,
        fetched / denom
      )

      if (items.length >= resp.total || resp.items.length === 0) break
      page++
    }
    dataset.set(tab, items)
  }

  return dataset
}

/** 读回任务快照：报表 + 图片任务清单，二者必须同源 */
async function loadResumeSnapshot(
  taskId: string
): Promise<ResumeSnapshot | null> {
  // 只按 key 取任务清单，不要 getAllArtifacts：那会把几百张图片一起读进内存，
  // 续传一开始就先把内存打满。
  const jobsBytes = await exportDb.getArtifact(taskId, JOBS_ARTIFACT_KEY)
  if (!jobsBytes) return null

  let snapshot: JobsSnapshot
  try {
    snapshot = jobsSnapshotSchema.parse(JSON.parse(strFromU8(jobsBytes)))
  } catch {
    // 旧版本或损坏的快照：回退到当前数据集，不要把用户卡死在恢复入口
    return null
  }

  const reports = new Map<RecordsTab, Uint8Array>()
  for (const tab of snapshot.tabs) {
    const bytes = await exportDb.getArtifact(taskId, reportKey(tab))
    if (bytes) reports.set(tab, bytes)
  }

  return {
    tabs: snapshot.tabs,
    jobs: snapshot.jobs,
    truncated: snapshot.truncated,
    reports,
  }
}

async function processImageJob(
  job: ImageJob,
  signal?: AbortSignal
): Promise<Uint8Array> {
  const hasOverlay = Object.keys(job.overlayOptions).length > 0
  const blob = await retryAsync(
    () =>
      hasOverlay
        ? burnOverlayOnImage(job.imagePath, { ...job.overlayOptions, signal })
        : fetchImageAsBlob(job.imagePath, signal),
    signal
  )
  return new Uint8Array(await blob.arrayBuffer())
}

function toZipBlob(zipData: Uint8Array): Blob {
  // fflate 的输出独占其 ArrayBuffer（offset 0、非 SharedArrayBuffer）。
  // 直接借用可以省掉几百 MB 的一次复制；条件不满足时才退回复制。
  if (
    zipData.byteOffset === 0 &&
    zipData.byteLength === zipData.buffer.byteLength
  ) {
    return new Blob([zipData.buffer as ArrayBuffer], {
      type: 'application/zip',
    })
  }
  return new Blob([new Uint8Array(zipData)], { type: 'application/zip' })
}

/** 归档内的报表说明文件，明确「这份证据包是什么、缺了什么」 */
function buildManifest(input: {
  taskId: string
  mode: 'selection' | 'batch'
  createdAt: number
  tabs: RecordsTab[]
  recordCount: number
  options: ExportDraftOptions
  reports: string[]
  jobCount: number
  exportedCount: number
  truncated: number
  failures: JobFailure[]
  warnings: string[]
}): ExportManifest {
  return {
    generator: 'zhulong-web',
    taskId: input.taskId,
    mode: input.mode,
    createdAt: new Date(input.createdAt).toISOString(),
    exportedAt: new Date().toISOString(),
    tabs: input.tabs,
    recordCount: input.recordCount,
    options: input.options,
    images: {
      jobs: input.jobCount,
      exported: input.exportedCount,
      failed: input.failures.length,
      truncated: input.truncated,
    },
    reports: input.reports,
    failures: input.failures,
    warnings: input.warnings,
  }
}

/**
 * 感知记录导出流水线核心执行入口
 */
export async function executeRecordsExport(
  options: ExportOptions
): Promise<ExportResult> {
  const {
    selectedRecords,
    queryConfig,
    exportCsv: exportCsvOption = true,
    exportImages: exportImagesOption = false,
    burnOverlay: burnOverlayOption = true,
    imageKind: imageKindOption = 'scene',
    maxImages: maxImagesOption,
    maxRecords = DEFAULT_MAX_RECORDS,
    resumeTaskId,
    signal,
    resolveCameraName,
    labels,
    onProgress,
  } = options

  assertNotAborted(signal)

  const emit: Emit = (phase, current, total, message, ratio) => {
    onProgress?.({
      phase,
      current,
      total,
      percent: percentFor(phase, ratio),
      message,
    })
  }

  emit('preparing', 0, 1, 'Preparing export...', 0)

  // 0. 续传优先按草稿快照重建：报表与图片清单都来自任务创建那一刻，
  //    绝不与当前 UI 的筛选条件混用。
  const draft = resumeTaskId ? await exportDb.getTaskDraft(resumeTaskId) : null
  const snapshot = draft ? await loadResumeSnapshot(draft.taskId) : null

  // 续传以草稿为准：报表与任务清单都来自任务创建那一刻的快照
  const exportCsv = snapshot
    ? (draft?.options.exportCsv ?? exportCsvOption)
    : exportCsvOption
  const exportImages = snapshot ? true : exportImagesOption
  const burnOverlay = snapshot
    ? (draft?.options.burnOverlay ?? burnOverlayOption)
    : burnOverlayOption
  const imageKind = snapshot
    ? (draft?.options.imageKind ?? imageKindOption)
    : imageKindOption
  const maxImages = snapshot
    ? (draft?.options.maxImages ?? maxImagesOption)
    : maxImagesOption

  // 归档说明里的告警在数据集解析阶段就可能产生（比如续传时的截断），
  // 所以提前声明
  const warnings: string[] = []

  let tabs: RecordsTab[]
  let dataset: Map<RecordsTab, RecordItem[]> | null = null
  let reportBytes = new Map<RecordsTab, Uint8Array>()
  let jobs: ImageJob[] = []
  let recordCount = 0
  let truncated = 0

  if (snapshot) {
    tabs = snapshot.tabs
    jobs = snapshot.jobs
    truncated = snapshot.truncated
    reportBytes = snapshot.reports
    recordCount = draft?.recordCount ?? 0
    if (truncated > 0) {
      warnings.push(
        `image jobs truncated: ${truncated} dropped by the image cap`
      )
    }
  } else if (selectedRecords) {
    tabs = [selectedRecords.tab]
    dataset = new Map([[selectedRecords.tab, selectedRecords.items]])
    recordCount = selectedRecords.items.length
    if (recordCount > maxRecords) {
      throw new ExportLimitError('records', maxRecords, recordCount)
    }
  } else if (queryConfig) {
    tabs = queryConfig.tabs
    // 固化时间快照，避免翻页期间新记录涌入导致重复/漂移
    const frozenEndTime = queryConfig.endTime || new Date().toISOString()
    // 先探一次各维度的 total：既能守住记录预算，也能让进度单调
    const expectedTotal = await probeRecordTotals(
      queryConfig,
      tabs,
      frozenEndTime,
      signal
    )
    if (expectedTotal > maxRecords) {
      throw new ExportLimitError('records', maxRecords, expectedTotal)
    }
    dataset = await fetchRecordsByQuery(
      queryConfig,
      tabs,
      frozenEndTime,
      expectedTotal,
      maxRecords,
      signal,
      emit
    )
    recordCount = tabs.reduce(
      (sum, tab) => sum + (dataset?.get(tab)?.length ?? 0),
      0
    )
  } else {
    throw new Error('Either selectedRecords or queryConfig must be provided')
  }

  // 1. 纯单 Tab 且仅导出 CSV：直接给单文件，不必绕 ZIP
  if (exportCsv && !exportImages && tabs.length === 1 && dataset) {
    const tab = tabs[0]
    const records = dataset.get(tab) ?? []
    emit('generating_csv', 1, 1, 'Generating CSV file...', 1)
    const csvString = exportRecordsToCsv(tab, records, resolveCameraName)
    emit('completed', 1, 1, 'Export completed', 1)
    return {
      blob: new Blob([csvString], { type: 'text/csv;charset=utf-8;' }),
      filename: `zhulong_${tab}_export_${localStamp()}.csv`,
      failedImages: 0,
      truncatedImages: 0,
    }
  }

  const mode: 'selection' | 'batch' = snapshot
    ? (draft?.mode ?? 'batch')
    : selectedRecords
      ? 'selection'
      : 'batch'
  const activeTab: RecordsTab = snapshot
    ? (draft?.activeTab ?? tabs[0])
    : tabs[0]
  const createdAt = draft?.createdAt ?? Date.now()
  const taskId = resumeTaskId ?? createTaskId()
  const failures: JobFailure[] = []
  const zipFiles: AsyncZippable = {}

  // 2. 报表：续传复用快照，首次生成后立刻落盘 —— 保证 CSV 与图片同源
  if (exportCsv) {
    if (reportBytes.size === 0 && dataset) {
      let index = 0
      for (const [tab, records] of dataset.entries()) {
        assertNotAborted(signal)
        const bytes = strToU8(
          exportRecordsToCsv(tab, records, resolveCameraName)
        )
        reportBytes.set(tab, bytes)
        // 图片任务需要断点续传：报表必须与任务清单一起固化。否则续传时只能拿
        // 「新查询生成的 CSV + 旧缓存里的图片」拼出一个看起来完全正常的假证据包。
        if (exportImages) {
          await exportDb.saveArtifact(taskId, reportKey(tab), bytes, 'text/csv')
        }
        index++
        emit(
          'generating_csv',
          index,
          dataset.size,
          `Generating ${tab} report...`,
          index / Math.max(dataset.size, 1)
        )
      }
    }
    for (const [tab, bytes] of reportBytes) {
      zipFiles[reportFileName(tab)] = [bytes, { level: 6 }]
    }
  }

  // 3. 图片任务：首次抽取后立即固化任务清单，续传不再依赖当前筛选
  const draftOptions: ExportDraftOptions = {
    exportCsv,
    exportImages,
    burnOverlay,
    imageKind,
    maxImages,
  }

  if (exportImages) {
    if (!snapshot) {
      if (!dataset) {
        throw new Error('Image export requires a resolved dataset')
      }
      for (const tab of tabs) {
        jobs.push(
          ...extractImageJobs(
            tab,
            dataset.get(tab) ?? [],
            imageKind,
            burnOverlay,
            labels
          )
        )
      }

      const cap = Math.min(
        maxImages && maxImages > 0 ? maxImages : HARD_MAX_IMAGES,
        HARD_MAX_IMAGES
      )
      if (jobs.length > cap) {
        truncated = jobs.length - cap
        jobs = jobs.slice(0, cap)
        warnings.push(
          `image jobs truncated: ${truncated} dropped by the ${cap}-image cap`
        )
      }

      const jobsSnapshot: JobsSnapshot = {
        version: 1,
        tabs,
        truncated,
        jobs: jobs.map((job) => ({
          ...job,
          // 已在 extractImageJobs 里求值成字符串；这里再显式剥离 signal
          overlayOptions: { ...job.overlayOptions, signal: undefined },
        })),
      }
      await exportDb.saveArtifact(
        taskId,
        JOBS_ARTIFACT_KEY,
        strToU8(JSON.stringify(jobsSnapshot)),
        'application/json'
      )
    }

    const completedSet = new Set<string>(draft?.completedKeys ?? [])
    let lastPersistedCount = completedSet.size
    let lastPersistedAt = Date.now()

    const draftBase = (): ExportTaskDraft => ({
      taskId,
      mode,
      activeTab,
      tabs,
      recordCount,
      totalCount: jobs.length,
      completedKeys: Array.from(completedSet),
      options: draftOptions,
      status: 'in_progress',
      createdAt,
      updatedAt: Date.now(),
    })

    const persistDraft = async (status: ExportTaskDraft['status']) => {
      try {
        await exportDb.saveTaskDraft({ ...draftBase(), status })
      } catch (err) {
        // 落盘失败不影响本次导出，但必须让用户从 manifest 里看到
        const warning = `draft persistence failed: ${describeError(err)}`
        if (!warnings.includes(warning)) warnings.push(warning)
      }
    }

    await persistDraft('in_progress')

    const persistIfDue = async () => {
      if (
        completedSet.size - lastPersistedCount < DRAFT_PERSIST_EVERY &&
        Date.now() - lastPersistedAt < DRAFT_PERSIST_INTERVAL_MS
      ) {
        return
      }
      lastPersistedCount = completedSet.size
      lastPersistedAt = Date.now()
      await persistDraft('in_progress')
    }

    const totalJobs = jobs.length
    let processed = 0
    const denominator = Math.max(totalJobs, 1)

    emit(
      'processing_images',
      0,
      totalJobs,
      `Processing images: 0 / ${totalJobs}`,
      completedSet.size / denominator
    )

    try {
      await runConcurrentPool(jobs, IMAGE_CONCURRENCY, signal, async (job) => {
        const filePath = jobArtifactPath(job)
        try {
          // 1. 优先命中 IndexedDB 缓存（断点接力）；命中即视为完成
          const cached = await exportDb.getArtifact(taskId, filePath)
          if (cached) {
            zipFiles[filePath] = [cached, { level: 0 }]
            completedSet.add(filePath)
            return
          }

          // 2. 拉取（必要时烧框），带退避重试
          const bytes = await processImageJob(job, signal)
          zipFiles[filePath] = [bytes, { level: 0 }]
          await exportDb.saveArtifact(taskId, filePath, bytes)
          completedSet.add(filePath)
          await persistIfDue()
        } catch (err) {
          // 取消不能被当成「单图失败」吞掉，必须冒泡中断整个流水线
          if (isAbortError(err)) throw err
          failures.push({
            tab: job.tab,
            recordId: job.recordId,
            kind: job.kind,
            artifact: filePath,
            reason: describeError(err),
          })
        } finally {
          processed++
          emit(
            'processing_images',
            processed,
            totalJobs,
            `Processing images: ${processed} / ${totalJobs}`,
            processed / denominator
          )
        }
      })
    } catch (err) {
      // 取消或致命错误：草稿置 paused，保留已完成产物供下次恢复
      await persistDraft('paused')
      throw err
    }

    await persistDraft('completed')

    // 下载真正开始后再清理临时数据，避免用户点取消时把断点也删掉
    setTimeout(() => {
      void exportDb.clearTaskDraft(taskId)
    }, 10_000)
  }

  // 4. 归档说明：缺图/截断/落盘失败都在这里显式声明
  if (failures.length > 0) {
    warnings.push(`${failures.length} image(s) failed and were skipped`)
  }
  zipFiles[MANIFEST_KEY] = [
    strToU8(
      JSON.stringify(
        buildManifest({
          taskId,
          mode,
          createdAt,
          tabs,
          recordCount,
          options: draftOptions,
          reports: Array.from(reportBytes.keys()).map(reportFileName),
          jobCount: jobs.length,
          exportedCount: jobs.length - failures.length,
          truncated,
          failures,
          warnings,
        }),
        null,
        2
      )
    ),
    { level: 6 },
  ]

  // 5. 打包
  emit('zipping', 0, 1, 'Compressing archive...', 0)

  const zipData = await new Promise<Uint8Array>((resolve, reject) => {
    zip(zipFiles, (err, data) => {
      if (err) reject(err)
      else resolve(data)
    })
  })

  emit('completed', 1, 1, 'Export completed', 1)

  return {
    blob: toZipBlob(zipData),
    filename: `zhulong_records_export_${localStamp()}.zip`,
    failedImages: failures.length,
    truncatedImages: truncated,
  }
}
