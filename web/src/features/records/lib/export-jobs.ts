import { z } from 'zod'
import { strFromU8 } from 'fflate'
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
import { exportDb, type ExportDraftOptions } from './export-db'
import {
  type ExportLabels,
  type ExportManifest,
  type ImageJob,
  type JobFailure,
  type OverlayLabelContext,
} from './export-types'

const REPORT_KEY_PREFIX = '__reports__/'
const REPORT_KEY_SUFFIX = '_records.csv'
export const JOBS_ARTIFACT_KEY = '__jobs__.json'

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
  truncated: z.number().int().nonnegative().default(0),
  jobs: z.array(imageJobSchema),
})

export type JobsSnapshot = z.infer<typeof jobsSnapshotSchema>

interface ResumeSnapshot {
  tabs: RecordsTab[]
  jobs: ImageJob[]
  truncated: number
  reports: Map<RecordsTab, Uint8Array>
}

function abortError(): DOMException {
  return new DOMException('Export cancelled', 'AbortError')
}

export function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === 'AbortError'
}

export function assertNotAborted(signal?: AbortSignal): void {
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

function isRetryable(err: unknown): boolean {
  if (isAbortError(err)) return false
  if (err instanceof MediaFetchError) {
    return err.status >= 500 || err.status === 408 || err.status === 429
  }
  return true
}

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

export function describeError(err: unknown): string {
  if (err instanceof Error) return err.message
  return String(err)
}

export function reportKey(tab: RecordsTab): string {
  return `${REPORT_KEY_PREFIX}${tab}${REPORT_KEY_SUFFIX}`
}

export function reportFileName(tab: RecordsTab): string {
  return `${tab}${REPORT_KEY_SUFFIX}`
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

export function extractImageJobs(
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

export function jobArtifactPath(job: ImageJob): string {
  const base = evidenceFileName(job.recordId, job.capturedAt).replace(
    /\.jpg$/i,
    ''
  )
  return `images/${job.tab}/${base}_${job.kind}.jpg`
}

export async function loadResumeSnapshot(
  taskId: string
): Promise<ResumeSnapshot | null> {
  const jobsBytes = await exportDb.getArtifact(taskId, JOBS_ARTIFACT_KEY)
  if (!jobsBytes) return null

  let snapshot: JobsSnapshot
  try {
    snapshot = jobsSnapshotSchema.parse(JSON.parse(strFromU8(jobsBytes)))
  } catch {
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

export async function processImageJob(
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

export function toZipBlob(zipData: Uint8Array): Blob {
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

export function buildManifest(input: {
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
