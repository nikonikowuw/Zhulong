import { type OverlayBurnOptions } from './canvas-overlay-drawer'
import { abortError } from './export-abort'
import {
  type ImageWorkerRequest,
  type ImageWorkerResponse,
} from './export-image-worker-protocol'
import { type ImageJob } from './export-types'
import { MediaFetchError } from './media-fetch'
import { type OverlayPaintOptions } from './overlay-paint'

/**
 * 导出图片 Worker 池。
 *
 * pipeline 的图片并发度就是池的大小：每个 Worker 一次处理一张图
 * （拉流 → 解码 → 烧录 → 编码），主线程只做消息收发。
 *
 * 取消语义：`signal` abort 时立即拒绝该任务的 Promise，但 Worker 里正在跑的
 * 那一张会继续跑完（结果丢弃）。要真正停掉 CPU，调 `terminateImageWorkerPool()`，
 * 它会终止所有 Worker 并拒绝全部在途任务；下一次 `run` 惰性重建。
 */

const POOL_SIZE = 3
const DEFAULT_MIME = 'image/jpeg'
const DEFAULT_QUALITY = 0.85

/** 运行环境不支持 Worker/OffscreenCanvas：调用方应整体回退主线程路径 */
export class WorkerUnsupportedError extends Error {
  constructor(message = 'Image worker is not supported in this environment') {
    super(message)
    this.name = 'WorkerUnsupportedError'
  }
}

interface Slot {
  worker: Worker
  pending: number
}

interface Inflight {
  reject: (err: unknown) => void
  cleanup: () => void
}

function toPaintOptions(options: OverlayBurnOptions): OverlayPaintOptions {
  return {
    primaryBox: options.primaryBox,
    primaryLabel: options.primaryLabel,
    secondaryBox: options.secondaryBox,
    secondaryLabel: options.secondaryLabel,
    palette: options.palette,
    secondaryPalette: options.secondaryPalette,
    landmarks: options.landmarks,
  }
}

function hasOverlay(job: ImageJob): boolean {
  return Object.keys(job.overlayOptions).length > 0
}

function toError(response: Extract<ImageWorkerResponse, { ok: false }>): Error {
  if (response.unsupported) {
    return new WorkerUnsupportedError(response.message)
  }
  if (typeof response.status === 'number') {
    return new MediaFetchError(response.status, response.message)
  }
  return new Error(response.message)
}

class ImageWorkerPool {
  private slots: Slot[] | null = null
  private readonly inflight = new Set<Inflight>()
  private nextId = 1

  private getSlots(): Slot[] {
    if (this.slots) return this.slots
    if (typeof Worker === 'undefined') {
      throw new WorkerUnsupportedError()
    }
    try {
      this.slots = Array.from({ length: POOL_SIZE }, () => ({
        worker: new Worker(
          new URL('./export-image.worker.ts', import.meta.url),
          {
            type: 'module',
          }
        ),
        pending: 0,
      }))
    } catch (err) {
      throw new WorkerUnsupportedError(
        err instanceof Error ? err.message : String(err)
      )
    }
    return this.slots
  }

  run(job: ImageJob, signal?: AbortSignal): Promise<Uint8Array> {
    if (signal?.aborted) {
      return Promise.reject(abortError())
    }

    const slots = this.getSlots()
    const slot = slots.reduce(
      (min, candidate) => (candidate.pending < min.pending ? candidate : min),
      slots[0]
    )
    const id = this.nextId++
    slot.pending++

    const request: ImageWorkerRequest = {
      id,
      imagePath: job.imagePath,
      overlay: hasOverlay(job) ? toPaintOptions(job.overlayOptions) : null,
      mimeType: DEFAULT_MIME,
      quality: DEFAULT_QUALITY,
    }

    return new Promise<Uint8Array>((resolve, reject) => {
      let settled = false
      const task: Inflight = { reject, cleanup: () => undefined }

      const settle = (fn: () => void) => {
        if (settled) return
        settled = true
        task.cleanup()
        this.inflight.delete(task)
        slot.pending--
        fn()
      }

      const onMessage = (event: MessageEvent<ImageWorkerResponse>) => {
        const data = event.data
        if (!data || data.id !== id) return
        if (data.ok) {
          settle(() => resolve(new Uint8Array(data.bytes)))
        } else {
          settle(() => reject(toError(data)))
        }
      }

      const onError = (event: ErrorEvent) => {
        settle(() => reject(new Error(event.message || 'Image worker crashed')))
      }

      const onAbort = () => settle(() => reject(abortError()))

      task.cleanup = () => {
        slot.worker.removeEventListener('message', onMessage)
        slot.worker.removeEventListener('error', onError)
        signal?.removeEventListener('abort', onAbort)
      }
      task.reject = (err) => settle(() => reject(err))

      slot.worker.addEventListener('message', onMessage)
      slot.worker.addEventListener('error', onError)
      signal?.addEventListener('abort', onAbort, { once: true })
      this.inflight.add(task)

      slot.worker.postMessage(request)
    })
  }

  terminate(): void {
    const slots = this.slots
    this.slots = null
    for (const slot of slots ?? []) {
      slot.worker.terminate()
    }
    for (const task of Array.from(this.inflight)) {
      this.inflight.delete(task)
      task.cleanup()
      task.reject(abortError())
    }
  }
}

const pool = new ImageWorkerPool()

/** 图片处理并发度：与池大小一致，避免任务排在 Worker 门口空等 */
export function getImageWorkerConcurrency(): number {
  return POOL_SIZE
}

/**
 * 在 Worker 池里跑一个图片任务。
 *
 * @throws {WorkerUnsupportedError} 环境不支持，调用方应回退主线程
 * @throws {MediaFetchError} 图片拉取失败（保留 HTTP 状态供重试策略判断）
 */
export function runImageJobInPool(
  job: ImageJob,
  signal?: AbortSignal
): Promise<Uint8Array> {
  return pool.run(job, signal)
}

/** 终止所有 Worker 并拒绝在途任务；取消导出或本轮任务结束时调用 */
export function terminateImageWorkerPool(): void {
  pool.terminate()
}
