/**
 * 导出流水线共享的取消/退避原语。
 *
 * 单独成模块是为了让 Worker 池、任务 store 都能用，而不用反向依赖
 * `export-jobs.ts`（那里拖着 zod 与 canvas 依赖，进首屏会白白变大）。
 */

export function abortError(): DOMException {
  return new DOMException('Export cancelled', 'AbortError')
}

export function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === 'AbortError'
}

export function assertNotAborted(signal?: AbortSignal): void {
  if (signal?.aborted) throw abortError()
}

export function delay(ms: number, signal?: AbortSignal): Promise<void> {
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
