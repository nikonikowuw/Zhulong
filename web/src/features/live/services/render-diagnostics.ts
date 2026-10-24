import { isWebCodecsDiagnosticsEnabled } from './webcodecs-decoder'

/**
 * 渲染循环的诊断采集器（`__ZHULONG_WEB_CODECS_DIAGNOSTICS__` 打开时生效）。
 *
 * 抽成独立模块的原因：这些计数器只是观测手段，却会把真正决定画面正确性的
 * 「缓冲最新帧 → rAF 绘制 → 关闭 VideoFrame」主流程挤到看不见的地方。
 * 诊断关闭时所有方法都是空操作，调用方无需分支。
 */
export interface RenderDiagnosticCounters {
  outputFrames: number
  pausedDrops: number
  replacedPendingFrames: number
  rafRequests: number
  rafCallbacks: number
  rafWithoutFrame: number
  framesWithoutCanvas: number
  framesWithoutContext: number
  drawCalls: number
  drawErrors: number
  frameCallbacks: number
  rafDelayTotalMs: number
  maxRafDelayMs: number
  drawTimeTotalMs: number
  maxDrawTimeMs: number
}

export type RenderDiagnosticCounter = keyof RenderDiagnosticCounters

const SUMMARY_INTERVAL_MS = 1000

function emptyCounters(): RenderDiagnosticCounters {
  return {
    outputFrames: 0,
    pausedDrops: 0,
    replacedPendingFrames: 0,
    rafRequests: 0,
    rafCallbacks: 0,
    rafWithoutFrame: 0,
    framesWithoutCanvas: 0,
    framesWithoutContext: 0,
    drawCalls: 0,
    drawErrors: 0,
    frameCallbacks: 0,
    rafDelayTotalMs: 0,
    maxRafDelayMs: 0,
    drawTimeTotalMs: 0,
    maxDrawTimeMs: 0,
  }
}

function average(total: number, count: number): number {
  if (count <= 0) return 0
  return Number((total / count).toFixed(1))
}

export interface RenderDiagnosticSession {
  /** 计数 +1；诊断关闭时为空操作。 */
  count(counter: RenderDiagnosticCounter): void
  /** 记录一次成功绘制及其耗时。 */
  recordDraw(durationMs: number): void
  /** 记录一次 rAF 实际调度延迟（请求 → 回调）。 */
  recordRafDelay(delayMs: number): void
  /** 记录一帧解码输出，并按秒输出汇总。 */
  recordOutputFrame(frame: VideoFrame, canvas: HTMLCanvasElement | null): void
  /** 取当前单调时钟；诊断关闭时返回 0，供调用方免分支计时。 */
  now(): number
}

/**
 * 打开一次诊断会话。返回的会话在诊断关闭时全部退化为空操作，
 * 但 `recordOutputFrame` 仍会清空窗口状态，避免重新打开时带着旧计数。
 */
export function createRenderDiagnosticSession(
  log: (
    level: 'info' | 'warn' | 'error',
    event: string,
    details: Record<string, unknown>
  ) => void
): RenderDiagnosticSession {
  let windowStart: number | null = null
  let counters = emptyCounters()
  let lastFrameSize: string | null = null

  const reset = () => {
    windowStart = null
    counters = emptyCounters()
    lastFrameSize = null
  }

  const flush = (now: number, canvas: HTMLCanvasElement | null) => {
    if (windowStart === null) return
    const durationMs = now - windowStart
    if (durationMs < SUMMARY_INTERVAL_MS) return

    const rect = canvas?.getBoundingClientRect()
    log('info', '1s_render_summary', {
      durationMs: Math.round(durationMs),
      ...counters,
      averageRafDelayMs: average(counters.rafDelayTotalMs, counters.rafCallbacks),
      averageDrawMs: average(counters.drawTimeTotalMs, counters.drawCalls),
      lastFrameSize,
      canvas: canvas
        ? {
            bufferWidth: canvas.width,
            bufferHeight: canvas.height,
            cssWidth: Math.round(rect?.width ?? 0),
            cssHeight: Math.round(rect?.height ?? 0),
          }
        : null,
    })
    windowStart = now
    counters = emptyCounters()
  }

  return {
    count(counter) {
      if (isWebCodecsDiagnosticsEnabled()) counters[counter]++
    },
    recordDraw(durationMs) {
      counters.drawCalls++
      counters.drawTimeTotalMs += durationMs
      counters.maxDrawTimeMs = Math.max(counters.maxDrawTimeMs, durationMs)
    },
    recordRafDelay(delayMs) {
      counters.rafDelayTotalMs += delayMs
      counters.maxRafDelayMs = Math.max(counters.maxRafDelayMs, delayMs)
    },
    recordOutputFrame(frame, canvas) {
      if (!isWebCodecsDiagnosticsEnabled()) {
        reset()
        return
      }
      const now = performance.now()
      if (windowStart === null) {
        windowStart = now
      }
      flush(now, canvas)
      counters.outputFrames++
      lastFrameSize = `${frame.displayWidth}x${frame.displayHeight}`
    },
    now() {
      return isWebCodecsDiagnosticsEnabled() ? performance.now() : 0
    },
  }
}
