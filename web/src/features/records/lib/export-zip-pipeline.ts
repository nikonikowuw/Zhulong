import { format } from 'date-fns'
import { strToU8, zip, type AsyncZippable } from 'fflate'
import { type RecordItem, type RecordsTab } from '../data/schema'
import { exportRecordsToCsv } from './export-csv'
import {
  exportDb,
  type ExportDraftOptions,
  type ExportTaskDraft,
} from './export-db'
import {
  assertNotAborted,
  buildManifest,
  describeError,
  extractImageJobs,
  isAbortError,
  jobArtifactPath,
  JOBS_ARTIFACT_KEY,
  loadResumeSnapshot,
  processImageJob,
  reportFileName,
  reportKey,
  toZipBlob,
  type JobsSnapshot,
} from './export-jobs'
import {
  fetchRecordsByQuery,
  probeRecordTotals,
  runConcurrentPool,
  type EmitExportProgress,
} from './export-records-fetcher'
import {
  DEFAULT_MAX_RECORDS,
  ExportLimitError,
  HARD_MAX_IMAGES,
  type ExportOptions,
  type ExportPhase,
  type ExportResult,
  type ImageJob,
  type JobFailure,
} from './export-types'

export {
  DEFAULT_MAX_RECORDS,
  ExportLimitError,
  HARD_MAX_IMAGES,
  type ExportLabels,
  type ExportOptions,
  type ExportPhase,
  type ExportProgress,
  type ExportQueryConfig,
  type ExportResult,
  type ImageJob,
  type OverlayLabelBuilder,
  type OverlayLabelContext,
} from './export-types'

const IMAGE_CONCURRENCY = 3
const MANIFEST_KEY = 'manifest.json'
const DRAFT_PERSIST_EVERY = 10
const DRAFT_PERSIST_INTERVAL_MS = 1_000

const PHASE_RANGE: Record<ExportPhase, [number, number]> = {
  preparing: [0, 2],
  fetching_records: [2, 38],
  generating_csv: [38, 46],
  processing_images: [46, 92],
  zipping: [92, 99],
  completed: [99, 100],
}

function percentFor(phase: ExportPhase, ratio: number): number {
  const [start, end] = PHASE_RANGE[phase]
  const safe = Number.isFinite(ratio) ? ratio : 0
  const clamped = Math.min(1, Math.max(0, safe))
  return Math.round(start + (end - start) * clamped)
}

function localStamp(date = new Date()): string {
  return format(date, 'yyyyMMdd_HHmmss')
}

function createTaskId(): string {
  const random =
    globalThis.crypto?.randomUUID?.() ?? Math.random().toString(36).slice(2, 10)
  return `task_${Date.now()}_${random}`
}

/** 感知记录导出流水线核心执行入口 */
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

  const progressLabels = labels?.progress
  const emit: EmitExportProgress = (phase, current, total, message, ratio) => {
    onProgress?.({
      phase,
      current,
      total,
      percent: percentFor(phase, ratio),
      message,
    })
  }

  emit('preparing', 0, 1, progressLabels?.preparing ?? 'Preparing export...', 0)

  const draft = resumeTaskId ? await exportDb.getTaskDraft(resumeTaskId) : null
  const snapshot = draft ? await loadResumeSnapshot(draft.taskId) : null

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
    const frozenEndTime = queryConfig.endTime || new Date().toISOString()
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
      emit,
      labels
    )
    recordCount = tabs.reduce(
      (sum, tab) => sum + (dataset?.get(tab)?.length ?? 0),
      0
    )
  } else {
    throw new Error('Either selectedRecords or queryConfig must be provided')
  }

  // 纯单 Tab 且仅导出 CSV：直接给单文件，不必绕 ZIP
  if (exportCsv && !exportImages && tabs.length === 1 && dataset) {
    const tab = tabs[0]
    const records = dataset.get(tab) ?? []
    emit(
      'generating_csv',
      1,
      1,
      progressLabels?.generatingCsvSingle ?? 'Generating CSV file...',
      1
    )
    const csvString = exportRecordsToCsv(tab, records, resolveCameraName)
    assertNotAborted(signal)
    emit('completed', 1, 1, progressLabels?.completed ?? 'Export completed', 1)
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

  if (exportCsv) {
    if (reportBytes.size === 0 && dataset) {
      let index = 0
      for (const [tab, records] of dataset.entries()) {
        assertNotAborted(signal)
        const bytes = strToU8(
          exportRecordsToCsv(tab, records, resolveCameraName)
        )
        reportBytes.set(tab, bytes)
        if (exportImages) {
          await exportDb.saveArtifact(taskId, reportKey(tab), bytes, 'text/csv')
        }
        index++
        emit(
          'generating_csv',
          index,
          dataset.size,
          progressLabels?.generatingCsv?.(tab) ?? `Generating ${tab} report...`,
          index / Math.max(dataset.size, 1)
        )
      }
    }
    for (const [tab, bytes] of reportBytes) {
      zipFiles[reportFileName(tab)] = [bytes, { level: 6 }]
    }
  }

  const draftOptions: ExportDraftOptions = {
    exportCsv,
    exportImages,
    burnOverlay,
    imageKind,
    maxImages,
  }

  let persistDraftStatus:
    | ((status: ExportTaskDraft['status']) => Promise<void>)
    | null = null

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

    const persistDraft = async (status: ExportTaskDraft['status']) => {
      try {
        await exportDb.saveTaskDraft({
          taskId,
          mode,
          activeTab,
          tabs,
          recordCount,
          totalCount: jobs.length,
          completedKeys: Array.from(completedSet),
          options: draftOptions,
          status,
          createdAt,
          updatedAt: Date.now(),
        })
      } catch (err) {
        const warning = `draft persistence failed: ${describeError(err)}`
        if (!warnings.includes(warning)) warnings.push(warning)
      }
    }
    persistDraftStatus = persistDraft

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
      progressLabels?.processingImages?.(0, totalJobs) ??
        `Processing images: 0 / ${totalJobs}`,
      completedSet.size / denominator
    )

    try {
      await runConcurrentPool(jobs, IMAGE_CONCURRENCY, signal, async (job) => {
        const filePath = jobArtifactPath(job)
        try {
          const cached = await exportDb.getArtifact(taskId, filePath)
          if (cached) {
            zipFiles[filePath] = [cached, { level: 0 }]
            completedSet.add(filePath)
            return
          }

          const bytes = await processImageJob(job, signal)
          zipFiles[filePath] = [bytes, { level: 0 }]
          await exportDb.saveArtifact(taskId, filePath, bytes)
          completedSet.add(filePath)
          await persistIfDue()
        } catch (err) {
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
            progressLabels?.processingImages?.(processed, totalJobs) ??
              `Processing images: ${processed} / ${totalJobs}`,
            processed / denominator
          )
        }
      })
    } catch (err) {
      await persistDraft('paused')
      throw err
    }
  }

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

  assertNotAborted(signal)
  emit('zipping', 0, 1, progressLabels?.zipping ?? 'Compressing archive...', 0)

  const zipData = await new Promise<Uint8Array>((resolve, reject) => {
    zip(zipFiles, (err, data) => {
      if (err) reject(err)
      else resolve(data)
    })
  })

  try {
    assertNotAborted(signal)
  } catch (err) {
    await persistDraftStatus?.('paused')
    throw err
  }

  if (persistDraftStatus) {
    await persistDraftStatus('completed')
    setTimeout(() => {
      void exportDb.clearTaskDraft(taskId).catch(() => undefined)
    }, 10_000)
  }

  emit('completed', 1, 1, progressLabels?.completed ?? 'Export completed', 1)

  return {
    blob: toZipBlob(zipData),
    filename: `zhulong_records_export_${localStamp()}.zip`,
    failedImages: failures.length,
    truncatedImages: truncated,
  }
}
