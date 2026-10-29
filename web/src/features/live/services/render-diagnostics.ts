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
  /** 被呈现调度器丢弃的帧（错过呈现窗口 / 超出队列上限 / 时间轴重建）。 */
  presentationDrops: number
  /** 呈现时钟因源流时间轴跳变而重锚的次数。 */
  clockRebases: number
  rafRequests: number
  rafCallbacks: number
  rafWithoutFrame: number
  framesWithoutCanvas: number
  framesWithoutContext: number
  drawCalls: number
  drawErrors: number
  frameCallbacks: number
  rafDelayTotalMs: number
  rafDelaySamples: number
  maxRafDelayMs: number
  rafIntervalTotalMs: number
  rafIntervalSamples: number
  maxRafIntervalMs: number
  outputCallbackIntervalTotalMs: number
  outputCallbackIntervalSamples: number
  maxOutputCallbackIntervalMs: number
  outputFrameIntervalTotalMs: number
  outputFrameIntervalSamples: number
  maxOutputFrameIntervalMs: number
  nonIncreasingFrameTimestamps: number
  drawIntervalTotalMs: number
  drawIntervalSamples: number
  maxDrawIntervalMs: number
  drawTimeTotalMs: number
  maxDrawTimeMs: number
}

export type RenderDiagnosticCounter = keyof RenderDiagnosticCounters

const SUMMARY_INTERVAL_MS = 1000

function emptyCounters(): RenderDiagnosticCounters {
  return {
    outputFrames: 0,
    pausedDrops: 0,
    presentationDrops: 0,
    clockRebases: 0,
    rafRequests: 0,
    rafCallbacks: 0,
    rafWithoutFrame: 0,
    framesWithoutCanvas: 0,
    framesWithoutContext: 0,
    drawCalls: 0,
    drawErrors: 0,
    frameCallbacks: 0,
    rafDelayTotalMs: 0,
    rafDelaySamples: 0,
    maxRafDelayMs: 0,
    rafIntervalTotalMs: 0,
    rafIntervalSamples: 0,
    maxRafIntervalMs: 0,
    outputCallbackIntervalTotalMs: 0,
    outputCallbackIntervalSamples: 0,
    maxOutputCallbackIntervalMs: 0,
    outputFrameIntervalTotalMs: 0,
    outputFrameIntervalSamples: 0,
    maxOutputFrameIntervalMs: 0,
    nonIncreasingFrameTimestamps: 0,
    drawIntervalTotalMs: 0,
    drawIntervalSamples: 0,
    maxDrawIntervalMs: 0,
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
  /** 记录一次成功绘制耗时和绘制时刻。 */
  recordDraw(durationMs: number, timestampMs: number): void
  /** 记录一次 rAF 回调及其与上次回调的间隔。 */
  recordRafCallback(timestampMs: number): void
  /** 记录一次 rAF 请求到回调的调度延迟。 */
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
  let lastOutputCallbackAt: number | null = null
  let lastFrameTimestampUs: number | null = null
  let lastRafCallbackAt: number | null = null
  let lastDrawAt: number | null = null

  const reset = () => {
    windowStart = null
    counters = emptyCounters()
    lastFrameSize = null
    lastOutputCallbackAt = null
    lastFrameTimestampUs = null
    lastRafCallbackAt = null
    lastDrawAt = null
  }

  const recordInterval = (
    intervalMs: number,
    totalKey:
      | 'rafIntervalTotalMs'
      | 'outputCallbackIntervalTotalMs'
      | 'outputFrameIntervalTotalMs'
      | 'drawIntervalTotalMs',
    samplesKey:
      | 'rafIntervalSamples'
      | 'outputCallbackIntervalSamples'
      | 'outputFrameIntervalSamples'
      | 'drawIntervalSamples',
    maxKey:
      | 'maxRafIntervalMs'
      | 'maxOutputCallbackIntervalMs'
      | 'maxOutputFrameIntervalMs'
      | 'maxDrawIntervalMs'
  ) => {
    if (!Number.isFinite(intervalMs) || intervalMs <= 0) return
    counters[totalKey] += intervalMs
    counters[samplesKey]++
    counters[maxKey] = Math.max(counters[maxKey], intervalMs)
  }

  const flush = (now: number, canvas: HTMLCanvasElement | null) => {
    if (windowStart === null) return
    const durationMs = now - windowStart
    if (durationMs < SUMMARY_INTERVAL_MS) return

    const rect = canvas?.getBoundingClientRect()
    log('info', '1s_render_summary', {
      durationMs: Math.round(durationMs),
      ...counters,
      averageRafDelayMs: average(
        counters.rafDelayTotalMs,
        counters.rafDelaySamples
      ),
      averageRafIntervalMs: average(
        counters.rafIntervalTotalMs,
        counters.rafIntervalSamples
      ),
      averageOutputCallbackIntervalMs: average(
        counters.outputCallbackIntervalTotalMs,
        counters.outputCallbackIntervalSamples
      ),
      averageOutputFrameIntervalMs: average(
        counters.outputFrameIntervalTotalMs,
        counters.outputFrameIntervalSamples
      ),
      averageDrawIntervalMs: average(
        counters.drawIntervalTotalMs,
        counters.drawIntervalSamples
      ),
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
    recordDraw(durationMs, timestampMs) {
      if (!isWebCodecsDiagnosticsEnabled()) return
      counters.drawCalls++
      counters.drawTimeTotalMs += durationMs
      counters.maxDrawTimeMs = Math.max(counters.maxDrawTimeMs, durationMs)
      if (lastDrawAt !== null) {
        recordInterval(
          timestampMs - lastDrawAt,
          'drawIntervalTotalMs',
          'drawIntervalSamples',
          'maxDrawIntervalMs'
        )
      }
      lastDrawAt = timestampMs
    },
    recordRafCallback(timestampMs) {
      if (!isWebCodecsDiagnosticsEnabled()) return
      counters.rafCallbacks++
      if (lastRafCallbackAt !== null) {
        recordInterval(
          timestampMs - lastRafCallbackAt,
          'rafIntervalTotalMs',
          'rafIntervalSamples',
          'maxRafIntervalMs'
        )
      }
      lastRafCallbackAt = timestampMs
    },
    recordRafDelay(delayMs) {
      if (!isWebCodecsDiagnosticsEnabled()) return
      counters.rafDelayTotalMs += delayMs
      counters.rafDelaySamples++
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
      if (lastOutputCallbackAt !== null) {
        recordInterval(
          now - lastOutputCallbackAt,
          'outputCallbackIntervalTotalMs',
          'outputCallbackIntervalSamples',
          'maxOutputCallbackIntervalMs'
        )
      }
      if (lastFrameTimestampUs !== null) {
        const frameIntervalMs = (frame.timestamp - lastFrameTimestampUs) / 1000
        if (frameIntervalMs > 0) {
          recordInterval(
            frameIntervalMs,
            'outputFrameIntervalTotalMs',
            'outputFrameIntervalSamples',
            'maxOutputFrameIntervalMs'
          )
        } else {
          counters.nonIncreasingFrameTimestamps++
        }
      }
      lastOutputCallbackAt = now
      lastFrameTimestampUs = frame.timestamp
      counters.outputFrames++
      lastFrameSize = `${frame.displayWidth}x${frame.displayHeight}`
    },
    now() {
      return isWebCodecsDiagnosticsEnabled() ? performance.now() : 0
    },
  }
}
