import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useExportJobStore } from './export-job-store'

const mocks = vi.hoisted(() => ({
  executeRecordsExport: vi.fn(),
  triggerDownload: vi.fn(),
  toast: {
    success: vi.fn(),
    warning: vi.fn(),
    error: vi.fn(),
    info: vi.fn(),
  },
}))

vi.mock('./export-zip-pipeline', () => ({
  executeRecordsExport: mocks.executeRecordsExport,
  ExportLimitError: class ExportLimitError extends Error {
    readonly scope = 'records'
    readonly limit = 1
    readonly actual = 2
  },
}))

vi.mock('@/lib/csv', () => ({
  triggerDownload: mocks.triggerDownload,
}))

vi.mock('sonner', () => ({ toast: mocks.toast }))

function resetStore() {
  useExportJobStore.setState({
    status: 'idle',
    progress: null,
    error: null,
    failedImages: 0,
    resumeRequested: false,
  })
}

/** 在真实浏览器里伪造页面可见性，验证“离开期间仍在推进”的告知 */
function setVisibility(state: 'hidden' | 'visible') {
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    get: () => state,
  })
  document.dispatchEvent(new Event('visibilitychange'))
}

describe('useExportJobStore', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    resetStore()
  })

  it('runs the export after the dialog is gone and downloads on completion', async () => {
    let resolveExport: ((value: unknown) => void) | undefined

    mocks.executeRecordsExport.mockImplementation(
      (options: {
        onProgress?: (progress: unknown) => void
        signal?: AbortSignal
      }) => {
        options.onProgress?.({
          phase: 'processing_images',
          current: 1,
          total: 4,
          percent: 25,
          message: 'processing',
        })
        expect(options.signal).toBeInstanceOf(AbortSignal)
        return new Promise((resolve) => {
          resolveExport = resolve
        })
      }
    )

    const started = useExportJobStore
      .getState()
      .start({ exportCsv: true, exportImages: true })

    expect(started).toBe(true)
    expect(useExportJobStore.getState().status).toBe('running')
    // 流水线是按需 `import()` 的：首帧进度要等模块加载完成才到
    await vi.waitFor(() => {
      expect(useExportJobStore.getState().progress?.percent).toBe(25)
    })

    resolveExport?.({
      blob: new Blob(['zip']),
      filename: 'export.zip',
      failedImages: 0,
      truncatedImages: 0,
    })

    await vi.waitFor(() => {
      expect(useExportJobStore.getState().status).toBe('completed')
    })
    expect(mocks.triggerDownload).toHaveBeenCalledTimes(1)
    expect(mocks.toast.success).toHaveBeenCalledTimes(1)
  })

  it('rejects a second export while one is still running', () => {
    mocks.executeRecordsExport.mockReturnValue(new Promise(() => undefined))

    expect(useExportJobStore.getState().start({ exportCsv: true })).toBe(true)
    expect(useExportJobStore.getState().start({ exportCsv: true })).toBe(false)
  })

  it('cancels the running job and aborts its signal', async () => {
    let signal: AbortSignal | undefined

    mocks.executeRecordsExport.mockImplementation(
      (options: { signal: AbortSignal }) => {
        signal = options.signal
        return new Promise((_, reject) => {
          options.signal.addEventListener('abort', () => {
            reject(new DOMException('aborted', 'AbortError'))
          })
        })
      }
    )

    useExportJobStore.getState().start({ exportCsv: true })
    useExportJobStore.getState().cancel()

    expect(useExportJobStore.getState().status).toBe('cancelled')
    // 流水线模块按需加载，signal 在 import 完成后才交给它
    await vi.waitFor(() => {
      expect(signal?.aborted).toBe(true)
    })
    expect(mocks.toast.info).toHaveBeenCalledTimes(1)

    // abort 引发的 reject 不应把状态改回 failed
    await Promise.resolve()
    expect(useExportJobStore.getState().status).toBe('cancelled')
    expect(mocks.toast.error).not.toHaveBeenCalled()
  })

  it('keeps the failure reason so the indicator can show it', async () => {
    mocks.executeRecordsExport.mockRejectedValue(new Error('network down'))

    useExportJobStore.getState().start({ exportCsv: true })

    await vi.waitFor(() => {
      expect(useExportJobStore.getState().status).toBe('failed')
    })
    expect(useExportJobStore.getState().error).toContain('network down')
    expect(mocks.toast.error).toHaveBeenCalledTimes(1)
  })

  it('does not let dismiss clear a running job', () => {
    mocks.executeRecordsExport.mockReturnValue(new Promise(() => undefined))

    useExportJobStore.getState().start({ exportCsv: true })
    useExportJobStore.getState().dismiss()

    expect(useExportJobStore.getState().status).toBe('running')
  })

  it('reports how much progress was made while the tab stayed hidden', async () => {
    let emitProgress: ((progress: unknown) => void) | undefined
    mocks.executeRecordsExport.mockImplementation(
      (options: { onProgress?: (progress: unknown) => void }) => {
        emitProgress = options.onProgress
        return new Promise(() => undefined)
      }
    )

    useExportJobStore.getState().start({ exportCsv: true, exportImages: true })
    await vi.waitFor(() => {
      expect(emitProgress).toBeTypeOf('function')
    })

    emitProgress?.({
      phase: 'processing_images',
      current: 1,
      total: 10,
      saved: 1,
    })
    await vi.waitFor(() => {
      expect(useExportJobStore.getState().progress?.saved).toBe(1)
    })

    setVisibility('hidden')
    emitProgress?.({
      phase: 'processing_images',
      current: 4,
      total: 10,
      saved: 4,
    })
    await vi.waitFor(() => {
      expect(useExportJobStore.getState().progress?.current).toBe(4)
    })

    setVisibility('visible')

    expect(mocks.toast.info).toHaveBeenCalledTimes(1)
    expect(mocks.toast.info.mock.calls[0][0]).toContain('3')
    expect(mocks.toast.info.mock.calls[0][0]).toContain('张')

    // 复位：任务永不 settle，不取消会把 listener 带到下一个用例
    useExportJobStore.getState().cancel()
  })

  it('stays silent when nothing happened while the tab was hidden', async () => {
    let emitProgress: ((progress: unknown) => void) | undefined
    mocks.executeRecordsExport.mockImplementation(
      (options: { onProgress?: (progress: unknown) => void }) => {
        emitProgress = options.onProgress
        return new Promise(() => undefined)
      }
    )

    useExportJobStore.getState().start({ exportCsv: true, exportImages: true })
    await vi.waitFor(() => {
      expect(emitProgress).toBeTypeOf('function')
    })

    emitProgress?.({
      phase: 'processing_images',
      current: 2,
      total: 10,
      saved: 2,
    })
    await vi.waitFor(() => {
      expect(useExportJobStore.getState().progress?.current).toBe(2)
    })

    setVisibility('hidden')
    setVisibility('visible')

    expect(mocks.toast.info).not.toHaveBeenCalled()

    useExportJobStore.getState().cancel()
  })

  it('exposes a one-shot resume request for the records page', () => {
    expect(useExportJobStore.getState().resumeRequested).toBe(false)

    useExportJobStore.getState().requestResume()
    expect(useExportJobStore.getState().resumeRequested).toBe(true)

    useExportJobStore.getState().consumeResumeRequest()
    expect(useExportJobStore.getState().resumeRequested).toBe(false)
  })
})
