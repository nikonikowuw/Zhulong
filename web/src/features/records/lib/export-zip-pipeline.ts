import { strToU8, zip } from 'fflate'
import { recordsApi } from '../api/records-api'
import {
  type Alarm,
  type Capture,
  type Recognition,
  type RecordItem,
  type RecordsTab,
} from '../data/schema'
import {
  burnOverlayOnImage,
  fetchImageAsBlob,
  type OverlayBurnOptions,
} from './canvas-overlay-drawer'
import { type CameraNameResolver, exportRecordsToCsv } from './export-csv'
import { exportDb } from './export-db'

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
  message?: string
}

export interface ExportOptions {
  /** 模式一：勾选导出（严格限定在当前选中的 Tab 与记录） */
  selectedRecords?: {
    tab: RecordsTab
    items: RecordItem[]
  }
  /** 模式二：全局条件导出（支持多 Tab 组合） */
  queryConfig?: {
    tabs: RecordsTab[]
    cameraId?: string
    startTime?: string
    endTime?: string
    matched?: boolean
    personId?: string
    alarmType?: string
    targetType?: string
  }
  /** 导出 CSV 开关，默认 true */
  exportCsv?: boolean
  /** 导出图片开关，默认 false */
  exportImages?: boolean
  /** 是否在图片上烧录检测框与研判标签，默认 true */
  burnOverlay?: boolean
  /** 图片导出类型: 'scene' (全景), 'closeup' (特写), 'both' (两者)，默认 'scene' */
  imageKind?: 'scene' | 'closeup' | 'both'
  /** 最大导出图片张数限制，避免大图压垮浏览器或传输太慢 */
  maxImages?: number
  /** 断点续传任务 ID */
  resumeTaskId?: string
  /** 取消信号 */
  signal?: AbortSignal
  /** 摄像机名称解析函数 */
  resolveCameraName?: CameraNameResolver
  /** 实时进度回调 */
  onProgress?: (progress: ExportProgress) => void
}

export interface ExportResult {
  blob: Blob
  filename: string
  isZip: boolean
}

interface ImageJob {
  tab: RecordsTab
  recordId: string
  kind: 'scene' | 'closeup'
  imagePath: string
  overlayOptions: OverlayBurnOptions
}

/**
 * 单操作网络指数退避重试执行器
 */
async function retryAsync<T>(
  fn: () => Promise<T>,
  retries = 3,
  delayMs = 300
): Promise<T> {
  let lastError: unknown
  for (let attempt = 0; attempt < retries; attempt++) {
    try {
      return await fn()
    } catch (err) {
      lastError = err
      if (attempt < retries - 1) {
        await new Promise((res) =>
          setTimeout(res, delayMs * Math.pow(2, attempt))
        )
      }
    }
  }
  throw lastError
}

/**
 * 触发浏览器动态下载
 */
export function triggerDownload(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/**
 * 提取记录中的图片导出任务
 */
function extractImageJobs(
  tab: RecordsTab,
  records: RecordItem[],
  imageKind: 'scene' | 'closeup' | 'both',
  burnOverlay: boolean
): ImageJob[] {
  const jobs: ImageJob[] = []

  for (const item of records) {
    if (tab === 'recognitions') {
      const rec = item as Recognition
      const overlayOptions: OverlayBurnOptions = burnOverlay
        ? {
            primaryBox: rec.box,
            primaryLabel: rec.personName
              ? `${rec.personName} (${(rec.similarity * 100).toFixed(0)}%)`
              : rec.matched
                ? `Matched (${(rec.similarity * 100).toFixed(0)}%)`
                : 'Stranger',
            palette: rec.matched ? 'emerald' : 'rose',
            secondaryBox: rec.bodyBox,
            secondaryLabel: 'Person',
            secondaryPalette: 'violet',
          }
        : {}

      if ((imageKind === 'scene' || imageKind === 'both') && rec.imagePath) {
        jobs.push({
          tab,
          recordId: rec.id,
          kind: 'scene',
          imagePath: rec.imagePath,
          overlayOptions,
        })
      }
      if (
        (imageKind === 'closeup' || imageKind === 'both') &&
        rec.faceImagePath
      ) {
        // 特写截图通常无需再重复画全身框
        jobs.push({
          tab,
          recordId: rec.id,
          kind: 'closeup',
          imagePath: rec.faceImagePath,
          overlayOptions: {},
        })
      }
    } else if (tab === 'alarms') {
      const alarm = item as Alarm
      const overlayOptions: OverlayBurnOptions = burnOverlay
        ? {
            primaryBox: alarm.box,
            primaryLabel: `${alarm.alarmType} (${(alarm.score * 100).toFixed(0)}%)`,
            palette: 'amber',
          }
        : {}

      if ((imageKind === 'scene' || imageKind === 'both') && alarm.imagePath) {
        jobs.push({
          tab,
          recordId: alarm.id,
          kind: 'scene',
          imagePath: alarm.imagePath,
          overlayOptions,
        })
      }
      if ((imageKind === 'closeup' || imageKind === 'both') && alarm.cropPath) {
        jobs.push({
          tab,
          recordId: alarm.id,
          kind: 'closeup',
          imagePath: alarm.cropPath,
          overlayOptions: {},
        })
      }
    } else {
      // captures 或 vehicles
      const cap = item as Capture
      const overlayOptions: OverlayBurnOptions = burnOverlay
        ? {
            primaryBox: cap.box,
            primaryLabel: `${cap.targetType} (${(cap.score * 100).toFixed(0)}%)`,
            palette: 'sky',
          }
        : {}

      if ((imageKind === 'scene' || imageKind === 'both') && cap.imagePath) {
        jobs.push({
          tab,
          recordId: cap.id,
          kind: 'scene',
          imagePath: cap.imagePath,
          overlayOptions,
        })
      }
      if ((imageKind === 'closeup' || imageKind === 'both') && cap.cropPath) {
        jobs.push({
          tab,
          recordId: cap.id,
          kind: 'closeup',
          imagePath: cap.cropPath,
          overlayOptions: {},
        })
      }
    }
  }

  return jobs
}

/**
 * 受控并发工作池执行器
 */
async function runConcurrentPool<T, R>(
  items: T[],
  limit: number,
  signal: AbortSignal | undefined,
  fn: (item: T, index: number) => Promise<R>
): Promise<R[]> {
  const results: R[] = new Array(items.length)
  let currentIndex = 0

  const workers = Array.from(
    { length: Math.min(limit, items.length) },
    async () => {
      while (currentIndex < items.length) {
        if (signal?.aborted) {
          throw new DOMException('Operation cancelled', 'AbortError')
        }
        const index = currentIndex++
        results[index] = await fn(items[index], index)
      }
    }
  )

  await Promise.all(workers)
  return results
}

/**
 * 全量按条件逐页安全拉取记录数据（自动固化 endTime 快照防漂移）
 */
async function fetchAllRecordsByQuery(
  tab: RecordsTab,
  queryConfig: NonNullable<ExportOptions['queryConfig']>,
  signal?: AbortSignal,
  onProgress?: (progress: ExportProgress) => void
): Promise<RecordItem[]> {
  const items: RecordItem[] = []
  let page = 1
  const pageSize = 100
  let total: number

  // 固化时间快照
  const frozenEndTime = queryConfig.endTime || new Date().toISOString()

  while (true) {
    if (signal?.aborted) {
      throw new DOMException('Operation cancelled', 'AbortError')
    }

    if (tab === 'recognitions') {
      const resp = await recordsApi.getRecognitions({
        page,
        pageSize,
        cameraId: queryConfig.cameraId,
        personId: queryConfig.personId,
        matched: queryConfig.matched,
        startTime: queryConfig.startTime,
        endTime: frozenEndTime,
      })
      total = resp.total
      items.push(...resp.items)
    } else if (tab === 'alarms') {
      const resp = await recordsApi.getAlarms({
        page,
        pageSize,
        cameraId: queryConfig.cameraId,
        alarmType: queryConfig.alarmType,
        startTime: queryConfig.startTime,
        endTime: frozenEndTime,
      })
      total = resp.total
      items.push(...resp.items)
    } else {
      // captures 或 vehicles
      const resp = await recordsApi.getCaptures({
        page,
        pageSize,
        cameraId: queryConfig.cameraId,
        targetType: tab === 'vehicles' ? 'vehicle' : queryConfig.targetType,
        startTime: queryConfig.startTime,
        endTime: frozenEndTime,
      })
      total = resp.total
      items.push(...resp.items)
    }

    onProgress?.({
      phase: 'fetching_records',
      current: items.length,
      total: Math.max(items.length, total),
      message: `Fetching ${tab}... (${items.length}/${total})`,
    })

    if (items.length >= total || page * pageSize >= total) {
      break
    }
    page++
  }

  return items
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
    exportCsv = true,
    exportImages = false,
    burnOverlay = true,
    imageKind = 'scene',
    maxImages,
    signal,
    resolveCameraName,
    onProgress,
  } = options

  if (signal?.aborted) {
    throw new DOMException('Export cancelled', 'AbortError')
  }

  onProgress?.({
    phase: 'preparing',
    current: 0,
    total: 100,
    message: 'Preparing export...',
  })

  // 1. 组织并收集各 Tab 的数据集
  const datasetMap = new Map<RecordsTab, RecordItem[]>()

  if (selectedRecords) {
    datasetMap.set(selectedRecords.tab, selectedRecords.items)
  } else if (queryConfig) {
    for (const tab of queryConfig.tabs) {
      if (signal?.aborted) {
        throw new DOMException('Export cancelled', 'AbortError')
      }
      const records = await fetchAllRecordsByQuery(
        tab,
        queryConfig,
        signal,
        onProgress
      )
      datasetMap.set(tab, records)
    }
  } else {
    throw new Error('Either selectedRecords or queryConfig must be provided')
  }

  // 2. 纯单 Tab 且仅导出 CSV 场景下：直接导出单 CSV，无需打包 ZIP
  const isSingleTab = datasetMap.size === 1
  if (exportCsv && !exportImages && isSingleTab) {
    onProgress?.({
      phase: 'generating_csv',
      current: 50,
      total: 100,
      message: 'Generating CSV file...',
    })

    const [tab, records] = Array.from(datasetMap.entries())[0]
    const csvString = exportRecordsToCsv(tab, records, resolveCameraName)
    const blob = new Blob([csvString], { type: 'text/csv;charset=utf-8;' })
    const timestamp = new Date().toISOString().replace(/[:.]/g, '-')
    const filename = `zhulong_${tab}_export_${timestamp}.csv`

    onProgress?.({
      phase: 'completed',
      current: 100,
      total: 100,
      message: 'Export completed',
    })

    return { blob, filename, isZip: false }
  }

  // 3. 多 Tab 或含图片导出：走 ZIP 归档流水线
  const zipFiles: Record<string, Uint8Array> = {}

  // 3.1 生成各 Tab 的 CSV 文件并加入 ZIP 根目录
  if (exportCsv) {
    onProgress?.({
      phase: 'generating_csv',
      current: 10,
      total: 100,
      message: 'Generating CSV reports...',
    })

    for (const [tab, records] of datasetMap.entries()) {
      const csvStr = exportRecordsToCsv(tab, records, resolveCameraName)
      zipFiles[`${tab}_records.csv`] = strToU8(csvStr)
    }
  }

  // 3.2 若开启导出图片：受控并发处理与 Canvas 烧录
  if (exportImages) {
    let allJobs: ImageJob[] = []
    for (const [tab, records] of datasetMap.entries()) {
      allJobs.push(...extractImageJobs(tab, records, imageKind, burnOverlay))
    }

    if (maxImages && maxImages > 0 && allJobs.length > maxImages) {
      allJobs = allJobs.slice(0, maxImages)
    }

    const totalJobs = allJobs.length
    let completedJobs = 0

    // 初始化或复用任务 ID
    const taskId =
      options.resumeTaskId ||
      `task_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`

    // 读取已有草稿
    const existingDraft = await exportDb.getTaskDraft(taskId)
    const completedSet = new Set<string>(existingDraft?.completedKeys || [])

    await exportDb.saveTaskDraft({
      taskId,
      mode: selectedRecords ? 'selection' : 'batch',
      activeTab:
        selectedRecords?.tab || (queryConfig?.tabs[0] ?? 'recognitions'),
      totalCount: totalJobs,
      completedKeys: Array.from(completedSet),
      options,
      status: 'in_progress',
      createdAt: existingDraft?.createdAt || Date.now(),
      updatedAt: Date.now(),
    })

    onProgress?.({
      phase: 'processing_images',
      current: completedSet.size,
      total: totalJobs,
      message: `Processing images: ${completedSet.size} / ${totalJobs}`,
    })

    try {
      await runConcurrentPool(
        allJobs,
        3, // 并发数限制为 3
        signal,
        async (job) => {
          const filePath = `images/${job.tab}/${job.recordId}_${job.kind}.jpg`

          try {
            // 1. 优先从 IndexedDB 本地缓存中读取（断点接力续传）
            const cachedBytes = await exportDb.getArtifact(taskId, filePath)
            if (cachedBytes) {
              zipFiles[filePath] = cachedBytes
              return
            }

            // 2. 本地无缓存，走网络拉取并带自动重试
            let imageBlob: Blob
            if (burnOverlay && Object.keys(job.overlayOptions).length > 0) {
              imageBlob = await retryAsync(() =>
                burnOverlayOnImage(job.imagePath, {
                  ...job.overlayOptions,
                  signal,
                })
              )
            } else {
              imageBlob = await retryAsync(() =>
                fetchImageAsBlob(job.imagePath, signal)
              )
            }

            const arrayBuf = await imageBlob.arrayBuffer()
            const bytes = new Uint8Array(arrayBuf)
            zipFiles[filePath] = bytes

            // 3. 及时持久化到 IndexedDB
            await exportDb.saveArtifact(taskId, filePath, bytes)
            completedSet.add(filePath)
            void exportDb.saveTaskDraft({
              taskId,
              mode: selectedRecords ? 'selection' : 'batch',
              activeTab:
                selectedRecords?.tab ||
                (queryConfig?.tabs[0] ?? 'recognitions'),
              totalCount: totalJobs,
              completedKeys: Array.from(completedSet),
              options,
              status: 'in_progress',
              createdAt: existingDraft?.createdAt || Date.now(),
              updatedAt: Date.now(),
            })
          } catch (err) {
            // 单张图片下载或解码失败时不中断整个流水线，记录错误并跳过
            // eslint-disable-next-line no-console
            console.warn(
              `[Export Pipeline] Failed to process image ${job.imagePath}:`,
              err
            )
          } finally {
            completedJobs++
            onProgress?.({
              phase: 'processing_images',
              current: Math.max(completedJobs, completedSet.size),
              total: totalJobs,
              message: `Processing images: ${Math.max(completedJobs, completedSet.size)} / ${totalJobs}`,
            })
          }
        }
      )
    } catch (poolErr) {
      // 若中途被 Abort 取消或暂停，更新任务状态为 paused，保留已有草稿供下次恢复
      await exportDb.saveTaskDraft({
        taskId,
        mode: selectedRecords ? 'selection' : 'batch',
        activeTab:
          selectedRecords?.tab || (queryConfig?.tabs[0] ?? 'recognitions'),
        totalCount: totalJobs,
        completedKeys: Array.from(completedSet),
        options,
        status: 'paused',
        createdAt: existingDraft?.createdAt || Date.now(),
        updatedAt: Date.now(),
      })
      throw poolErr
    }

    // 成功完成全部图片处理
    await exportDb.saveTaskDraft({
      taskId,
      mode: selectedRecords ? 'selection' : 'batch',
      activeTab:
        selectedRecords?.tab || (queryConfig?.tabs[0] ?? 'recognitions'),
      totalCount: totalJobs,
      completedKeys: Array.from(completedSet),
      options,
      status: 'completed',
      createdAt: existingDraft?.createdAt || Date.now(),
      updatedAt: Date.now(),
    })

    // 全量打包触发下载后自动清理 IndexedDB 临时数据
    setTimeout(() => {
      void exportDb.clearTaskDraft(taskId)
    }, 1500)
  }

  // 3.3 打包生成 ZIP
  onProgress?.({
    phase: 'zipping',
    current: 90,
    total: 100,
    message: 'Compressing archive...',
  })

  const zipData = await new Promise<Uint8Array>((resolve, reject) => {
    zip(zipFiles, (err, data) => {
      if (err) reject(err)
      else resolve(data)
    })
  })

  const timestamp = new Date().toISOString().replace(/[:.]/g, '-')
  const filename = `zhulong_records_export_${timestamp}.zip`
  const blob = new Blob([zipData as unknown as BlobPart], {
    type: 'application/zip',
  })

  onProgress?.({
    phase: 'completed',
    current: 100,
    total: 100,
    message: 'Export completed',
  })

  return { blob, filename, isZip: true }
}
