import { toast } from 'sonner'
import { create } from 'zustand'
import { triggerDownload } from '@/lib/csv'
import i18n from '@/lib/i18n'
import { isAbortError } from './export-abort'
import { terminateImageWorkerPool } from './export-image-worker-pool'
import {
  ExportLimitError,
  type ExportOptions,
  type ExportProgress,
} from './export-types'

/**
 * 感知记录导出任务的进程内状态。
 *
 * 导出流水线完全跑在浏览器里，但任务生命周期**不再绑定导出对话框**：
 * 一旦点击「开始导出」，弹窗可以关闭，用户继续翻页/看详情/切路由，
 * 全局进度卡与 toast 负责反馈，完成后自动触发下载。
 *
 * 图片处理跑在 Worker 池里（`export-image-worker-pool.ts`），主线程只更新
 * 进度与写 IndexedDB；流水线模块按需 `import()`，所以挂载全局进度卡的布局
 * 不会把 fflate/zod 拖进首屏。
 *
 * 只允许单任务并发：浏览器端同时跑两条流水线会互相拖慢，也会争抢同一份
 * IndexedDB 草稿。
 */
export type ExportJobStatus =
  | 'idle'
  | 'running'
  | 'completed'
  | 'failed'
  | 'cancelled'

export type ExportJobStarter = (
  options: Omit<ExportOptions, 'signal' | 'onProgress'>
) => boolean

export interface ExportJobState {
  status: ExportJobStatus
  progress: ExportProgress | null
  error: string | null
  failedImages: number
  /**
   * 跨页面的“去继续上次导出”请求：不挂在布局上的全局进度卡发出，
   * 由记录页消费后打开导出对话框（那里才有续传 banner 的完整上下文）。
   */
  resumeRequested: boolean
  start: ExportJobStarter
  cancel: () => void
  dismiss: () => void
  requestResume: () => void
  consumeResumeRequest: () => void
}

const INITIAL_PROGRESS: ExportProgress = {
  phase: 'preparing',
  current: 0,
  total: 1,
  percent: 0,
}

/** 进度落库的最小间隔：几百张图时逐张 set 会让全局进度卡每帧重渲染 */
const PROGRESS_FLUSH_MS = 150

let activeController: AbortController | null = null
let runToken = 0
let unloadGuardAttached = false

/** 已处理图片数（用于“离开期间进展”提示，阶段切换后 current 会归零） */
let lastImageCount = 0
/** 用户切走时已处理的图片数 */
let hiddenImageCount = 0
let visibilityWatcherAttached = false

function handleBeforeUnload(event: BeforeUnloadEvent) {
  event.preventDefault()
  // 旧浏览器需要 returnValue 才会弹确认框
  event.returnValue = ''
}

/** 任务在跑时拦截刷新/关闭：导出进度只存在这个页面里，刷新即中断。 */
function attachUnloadGuard() {
  if (unloadGuardAttached || typeof window === 'undefined') return
  window.addEventListener('beforeunload', handleBeforeUnload)
  unloadGuardAttached = true
}

function detachUnloadGuard() {
  if (!unloadGuardAttached || typeof window === 'undefined') return
  window.removeEventListener('beforeunload', handleBeforeUnload)
  unloadGuardAttached = false
}

/**
 * 用户切走 / 切回时告知真相。
 *
 * 切到别的标签页**不会**中断导出，只会让主线程定时器被节流 —— 但用户看不见
 * 进度就会怀疑任务已经死掉，回来时直接刷新。把“离开期间又做了 N 张”说出来，
 * 比任何书面提示都更能建立信任。
 */
function handleVisibilityChange() {
  if (typeof document === 'undefined') return

  if (document.visibilityState === 'hidden') {
    hiddenImageCount = lastImageCount
    return
  }

  const delta = lastImageCount - hiddenImageCount
  hiddenImageCount = lastImageCount
  if (delta <= 0) return
  if (useExportJobStore.getState().status !== 'running') return

  toast.info(translate('leaveResumed', { count: delta }))
}

function attachVisibilityWatcher() {
  if (visibilityWatcherAttached || typeof document === 'undefined') return
  document.addEventListener('visibilitychange', handleVisibilityChange)
  visibilityWatcherAttached = true
}

function detachVisibilityWatcher() {
  if (!visibilityWatcherAttached || typeof document === 'undefined') return
  document.removeEventListener('visibilitychange', handleVisibilityChange)
  visibilityWatcherAttached = false
}

function translate(key: string, options?: Record<string, unknown>): string {
  return String(i18n.t(`records:export.${key}`, options))
}

/**
 * 进度节流：阶段切换立即发布，同一阶段内按时间窗合并，尾部更新不丢。
 * 后台标签页里 setTimeout 会被节流到 ~1s，这是可接受的代价 —— 用户切回来
 * 时仍能看到最新进度，而不是停在某一帧。
 */
function createProgressPublisher(publish: (progress: ExportProgress) => void) {
  let lastFlushAt = 0
  let timer: ReturnType<typeof setTimeout> | null = null
  let pending: ExportProgress | null = null

  const flush = () => {
    timer = null
    if (!pending) return
    const next = pending
    pending = null
    lastFlushAt = Date.now()
    publish(next)
  }

  return {
    push(progress: ExportProgress) {
      const phaseChanged = pending?.phase !== progress.phase
      pending = progress

      if (phaseChanged) {
        if (timer) clearTimeout(timer)
        timer = null
        flush()
        return
      }

      const elapsed = Date.now() - lastFlushAt
      if (elapsed >= PROGRESS_FLUSH_MS) {
        if (timer) clearTimeout(timer)
        timer = null
        flush()
      } else if (!timer) {
        timer = setTimeout(flush, PROGRESS_FLUSH_MS - elapsed)
      }
    },
    dispose() {
      if (timer) clearTimeout(timer)
      timer = null
      pending = null
    },
  }
}

export const useExportJobStore = create<ExportJobState>()((set, get) => ({
  status: 'idle',
  progress: null,
  error: null,
  failedImages: 0,
  resumeRequested: false,

  start: (options) => {
    if (get().status === 'running') {
      return false
    }

    const token = ++runToken
    const controller = new AbortController()
    activeController = controller
    lastImageCount = 0
    hiddenImageCount = 0
    attachUnloadGuard()
    attachVisibilityWatcher()

    set({
      status: 'running',
      progress: INITIAL_PROGRESS,
      error: null,
      failedImages: 0,
    })

    void (async () => {
      const publisher = createProgressPublisher((progress) => {
        if (token !== runToken) return
        if (progress.phase === 'processing_images') {
          lastImageCount = progress.current
        }
        set({ progress })
      })

      try {
        const { executeRecordsExport } = await import('./export-zip-pipeline')
        const result = await executeRecordsExport({
          ...options,
          signal: controller.signal,
          onProgress: (progress) => publisher.push(progress),
        })

        if (token !== runToken) return

        triggerDownload(result.blob, result.filename)
        set({
          status: 'completed',
          progress: { ...INITIAL_PROGRESS, phase: 'completed', percent: 100 },
          failedImages: result.failedImages,
        })

        if (result.failedImages > 0) {
          toast.warning(
            translate('partialFailure', { count: result.failedImages })
          )
        } else {
          toast.success(translate('success'))
        }
      } catch (err) {
        if (isAbortError(err)) {
          if (token === runToken) {
            set({ status: 'cancelled', progress: null })
          }
          return
        }

        const message =
          err instanceof ExportLimitError
            ? translate('tooManyRecords', {
                total: err.actual,
                limit: err.limit,
              })
            : translate('failedWithReason', {
                reason: err instanceof Error ? err.message : String(err),
              })

        if (token === runToken) {
          set({ status: 'failed', progress: null, error: message })
        }
        toast.error(message)
      } finally {
        publisher.dispose()
        terminateImageWorkerPool()
        if (token === runToken) {
          activeController = null
          detachUnloadGuard()
          detachVisibilityWatcher()
        }
      }
    })()

    return true
  },

  cancel: () => {
    if (get().status !== 'running') return

    // 先让旧 token 失效，避免 abort 引发的 catch/finally 覆盖这里设置的状态
    runToken++
    activeController?.abort()
    activeController = null
    // 立即停掉 Worker 里的解码/编码，别让取消后还继续烧 CPU
    terminateImageWorkerPool()
    detachUnloadGuard()
    detachVisibilityWatcher()
    set({ status: 'cancelled', progress: null })
    toast.info(translate('cancelled'))
  },

  dismiss: () => {
    if (get().status === 'running') return
    set({ status: 'idle', progress: null, error: null, failedImages: 0 })
  },

  requestResume: () => set({ resumeRequested: true }),

  consumeResumeRequest: () => set({ resumeRequested: false }),
}))
