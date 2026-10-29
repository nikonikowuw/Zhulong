/**
 * 预览链路的可观测性采集器：**起播冷启动** 与 **稳态健康** 两类日志的唯一出口。
 *
 * 拆出来的原因：这两件事都只是「读解码器状态 + 计数 + 周期 flush」，
 * 却曾和渲染循环的排期、VideoFrame 释放混在同一个 effect 里的同一组闭包变量中，
 * 于是出现「首帧成功时顺手把 health 定时器也停掉」这类跨界误伤，且无法单测。
 * 本模块刻意不依赖 DOM、VideoFrame 与 rAF，只依赖注入的 now / report，便于表驱动覆盖。
 *
 * ⚠️ 总闸：`window.__ZHULONG_PREVIEW_TRACE__ = false` 可整体关闭本模块的两类日志。
 *    默认开启 —— 起播慢与「一直在顿」恰恰只能在现场复现，默认静音等于放弃现场证据。
 *    关掉时要能确认「确实关掉了」，而不是又变成玄学。
 */
import {
  logColdStart,
  logStreamHealth,
  STREAM_HEALTH_INTERVAL_MS,
  stageDeltas,
} from './cold-start-trace'

declare global {
  interface Window {
    /**
     * 预览追踪总闸：显式置 false 关闭 `[Zhulong][ColdStart]` 与 `[Zhulong][StreamHealth]`。
     * 不设置（undefined）视为开启。
     */
    __ZHULONG_PREVIEW_TRACE__?: boolean
  }
}

/** 迟迟不出画时的兜底上报窗口（毫秒）。RTSP 冷建连实测可达 4 秒，不能报太早。 */
const COLD_START_TIMEOUT_MS = 5000

/** 日志里默认的播放器标识。 */
const UNKNOWN_TRACE_ID = 'unknown'

/** 预览追踪是否开启（未设置视为开启）。 */
export function isPreviewTraceEnabled(): boolean {
  if (typeof window === 'undefined') return true
  return window.__ZHULONG_PREVIEW_TRACE__ !== false
}

/**
 * 采集器需要解码器暴露的最小视图。
 *
 * 用窄接口而不是 `StreamDecoder`，好处是单测不需要构造真实解码器与 VideoFrame。
 */
export interface TraceDecoderView {
  getFirstDecodeCallAtMs(): number | null
  getFirstSubmitAtMs(): number | null
  getFirstOutputAtMs(): number | null
  getSeqGapFrames(): number
  getDecodeQueueSize(): number
  /** 读取并清零解码队列峰值：单调上涨 = 解码吞吐不够，锯齿 = 只是成簇到达。 */
  takeDecodeQueuePeak(): number
  isSkipping(): boolean
  getStats(): { resyncs: number; rebuilds: number; errors: number }
}

export interface PreviewTelemetryOptions {
  /** 播放器标识（通常是 cameraId），按调用取值以免重建解码器。 */
  traceId: () => string
  /** 单调时钟，默认 performance.now。 */
  now?: () => number
  reportColdStart?: (event: string, details: Record<string, unknown>) => void
  reportHealth?: (details: Record<string, unknown>) => void
  healthIntervalMs?: number
  coldStartTimeoutMs?: number
  /** 呈现调度器当前积压帧数（稳态健康行用）。 */
  schedulerPending?: () => number
}

export interface PreviewTelemetry {
  /** 绑定解码器：启动冷启动兜底计时与稳态采样。 */
  attach(decoder: TraceDecoderView): void
  /** 记录一帧真正被画到 canvas。 */
  countDrawnFrame(): void
  /** 记录因呈现窗口/队列上限/时间轴重锚被丢弃的帧。 */
  countPresentationDrop(count?: number): void
  /** 记录一次呈现时钟重锚。 */
  countClockRebase(count?: number): void
  /** 首次出画：上报一次起播拆分，并让稳态观测进入正式窗口。返回是否首次。 */
  markFirstDraw(atMs: number): boolean
  /** 停止计时并解绑解码器（可重复调用）。 */
  dispose(): void
}

/**
 * 创建预览遥测采集器。
 *
 * 只创建一次、随渲染循环一起销毁；`attach` 在解码器构造完成后调用。
 */
export function createPreviewTelemetry(
  options: PreviewTelemetryOptions
): PreviewTelemetry {
  const now = options.now ?? (() => performance.now())
  const reportColdStart = options.reportColdStart ?? logColdStart
  const reportHealth = options.reportHealth ?? logStreamHealth
  const healthIntervalMs = options.healthIntervalMs ?? STREAM_HEALTH_INTERVAL_MS
  const coldStartTimeoutMs = options.coldStartTimeoutMs ?? COLD_START_TIMEOUT_MS
  const enabled = isPreviewTraceEnabled()

  let decoder: TraceDecoderView | null = null
  let coldStartReported = false
  let firstDrawAt: number | null = null
  let coldStartTimer: ReturnType<typeof setTimeout> | null = null
  let healthTimer: ReturnType<typeof setInterval> | null = null

  // 稳态窗口：首次解码输出之前一律不计（否则冷启动的等待会混进「帧率」）。
  let healthStarted = false
  let windowStartedAt = 0
  let windowDrawnFrames = 0
  let windowPresentationDrops = 0
  let windowClockRebases = 0
  let windowSeqGapBase = 0

  const traceId = (): string => options.traceId() || UNKNOWN_TRACE_ID

  const clearColdStartTimer = (): void => {
    if (coldStartTimer !== null) {
      clearTimeout(coldStartTimer)
      coldStartTimer = null
    }
  }

  /**
   * 上报一次起播结果（每次渲染循环最多一次）。
   *
   * ⚠️ 这里**只**停冷启动兜底计时，绝不动 health 定时器：
   *    首次出画正是稳态观测的起点，早先把它一起 clear 掉，
   *    导致 `[Zhulong][StreamHealth]` 在健康链路上永远不产出。
   */
  function reportOutcome(outcome: 'drawn' | 'stalled'): void {
    const current = decoder
    if (coldStartReported || current === null) return
    coldStartReported = true
    clearColdStartTimer()
    if (!enabled) return

    const decodeCallAt = current.getFirstDecodeCallAtMs()
    reportColdStart(
      outcome === 'drawn' ? 'cold_start_render' : 'cold_start_render_stalled',
      {
        trace_id: traceId(),
        outcome,
        ...stageDeltas({
          originAtMs: decodeCallAt,
          stages: {
            decode_submitted_ms: current.getFirstSubmitAtMs(),
            frame_output_ms: current.getFirstOutputAtMs(),
            frame_drawn_ms: firstDrawAt,
          },
        }),
        decode_called: decodeCallAt !== null,
        skipping_at_report: current.isSkipping(),
        ...current.getStats(),
      }
    )
  }

  function emitHealth(): void {
    const current = decoder
    if (current === null || !enabled) return
    // 还没出过画：把窗口起点一再后移，稳态统计从「真正有帧」开始算。
    if (current.getFirstOutputAtMs() === null) return
    if (!healthStarted) {
      healthStarted = true
      windowStartedAt = now()
      windowSeqGapBase = current.getSeqGapFrames()
      return
    }

    const nowMs = now()
    const windowMs = Math.round(nowMs - windowStartedAt)
    const stats = current.getStats()
    reportHealth({
      trace_id: traceId(),
      window_ms: windowMs,
      frames_drawn: windowDrawnFrames,
      frames_drawn_fps:
        windowMs > 0
          ? Number(((windowDrawnFrames * 1000) / windowMs).toFixed(1))
          : 0,
      presentation_drops: windowPresentationDrops,
      // 服务端丢帧：seq 空洞。与 presentation_drops（前端丢）互为归因分界。
      seq_gap_frames: current.getSeqGapFrames() - windowSeqGapBase,
      clock_rebases: windowClockRebases,
      scheduler_pending: options.schedulerPending?.() ?? 0,
      decode_queue: current.getDecodeQueueSize(),
      // 峰值比瞬时值更能定位「一直在顿」：单调上涨说明解码吞吐不够，
      // 锯齿说明只是相机成簇到达（调整缓冲深度即可，不必怀疑机器性能）。
      decode_queue_peak: current.takeDecodeQueuePeak(),
      skipping: current.isSkipping(),
      resyncs: stats.resyncs,
      rebuilds: stats.rebuilds,
      errors: stats.errors,
    })

    windowDrawnFrames = 0
    windowPresentationDrops = 0
    windowClockRebases = 0
    windowSeqGapBase = current.getSeqGapFrames()
    windowStartedAt = nowMs
  }

  return {
    attach(current) {
      decoder = current
      if (!enabled) return
      // 迟迟不出画的兜底：报告已采集到的各段耗时 + 解码器状态。
      // 若 decode_called=false，说明数据包根本没到浏览器（问题在 WS 或服务端）。
      coldStartTimer = setTimeout(
        () => reportOutcome('stalled'),
        coldStartTimeoutMs
      )
      healthTimer = setInterval(emitHealth, healthIntervalMs)
    },
    countDrawnFrame() {
      windowDrawnFrames++
    },
    countPresentationDrop(count = 1) {
      windowPresentationDrops += count
    },
    countClockRebase(count = 1) {
      windowClockRebases += count
    },
    markFirstDraw(atMs) {
      if (firstDrawAt !== null) return false
      firstDrawAt = atMs
      reportOutcome('drawn')
      return true
    },
    dispose() {
      clearColdStartTimer()
      if (healthTimer !== null) {
        clearInterval(healthTimer)
        healthTimer = null
      }
      decoder = null
    },
  }
}
