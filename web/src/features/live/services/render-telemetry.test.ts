import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  createPreviewTelemetry,
  isPreviewTraceEnabled,
  type TraceDecoderView,
} from './render-telemetry'

/**
 * 遥测采集器的契约：
 * - 起播结果每次渲染循环只上报一次（出画或超时兜底二选一）；
 * - **首次出画不得掐断稳态健康行** —— 它正是稳态观测的起点；
 * - 稳态窗口只在真正解码出帧之后才开始，窗口内的计数会被 flush 归零。
 */
interface Report {
  event: string
  details: Record<string, unknown>
}

interface DecoderStub {
  view: TraceDecoderView
  setFirstOutput(ms: number | null): void
  setSeqGap(frames: number): void
}

function createDecoderStub(
  overrides: Partial<TraceDecoderView> = {}
): DecoderStub {
  const state: {
    firstOutputAtMs: number | null
    seqGapFrames: number
    decodeQueue: number
    skipping: boolean
    stats: { resyncs: number; rebuilds: number; errors: number }
  } = {
    firstOutputAtMs: null,
    seqGapFrames: 0,
    decodeQueue: 0,
    skipping: false,
    stats: { resyncs: 0, rebuilds: 0, errors: 0 },
  }

  const view: TraceDecoderView = {
    getFirstDecodeCallAtMs: () => 10,
    getFirstSubmitAtMs: () => 40,
    getFirstOutputAtMs: () => state.firstOutputAtMs,
    getSeqGapFrames: () => state.seqGapFrames,
    getDecodeQueueSize: () => state.decodeQueue,
    takeDecodeQueuePeak: () => state.decodeQueue,
    isSkipping: () => state.skipping,
    getStats: () => state.stats,
    ...overrides,
  }

  return {
    view,
    setFirstOutput(ms) {
      state.firstOutputAtMs = ms
    },
    setSeqGap(frames) {
      state.seqGapFrames = frames
    },
  }
}

function createHarness(options: { disabled?: boolean } = {}) {
  if (options.disabled) window.__ZHULONG_PREVIEW_TRACE__ = false
  let clock = 0
  const coldStart: Report[] = []
  const health: Report[] = []
  const telemetry = createPreviewTelemetry({
    traceId: () => 'cam-1',
    now: () => clock,
    healthIntervalMs: 1000,
    coldStartTimeoutMs: 5000,
    schedulerPending: () => 3,
    reportColdStart: (event, details) => coldStart.push({ event, details }),
    reportHealth: (details) => health.push({ event: 'stream_health', details }),
  })
  return {
    telemetry,
    coldStart,
    health,
    advance(ms: number) {
      clock += ms
      vi.advanceTimersByTime(ms)
    },
  }
}

describe('createPreviewTelemetry', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
    delete window.__ZHULONG_PREVIEW_TRACE__
  })

  it('首次出画上报一次起播拆分，并保留稳态健康行继续产出', () => {
    const harness = createHarness()
    const decoder = createDecoderStub()
    harness.telemetry.attach(decoder.view)
    decoder.setFirstOutput(70)

    harness.advance(120)
    expect(harness.telemetry.markFirstDraw(120)).toBe(true)
    // 同一渲染循环内第二次出画不该重复上报
    expect(harness.telemetry.markFirstDraw(200)).toBe(false)

    expect(harness.coldStart).toHaveLength(1)
    expect(harness.coldStart[0].event).toBe('cold_start_render')
    expect(harness.coldStart[0].details).toMatchObject({
      trace_id: 'cam-1',
      outcome: 'drawn',
      decode_submitted_ms: 30,
      frame_output_ms: 60,
      frame_drawn_ms: 110,
      decode_called: true,
    })

    // ⚠️ 回归保护：首次出画之后 health 定时器必须还活着。
    harness.advance(1000) // 建立稳态窗口起点
    harness.telemetry.countDrawnFrame()
    harness.advance(1000)
    expect(harness.health).toHaveLength(1)
    expect(harness.health[0].details).toMatchObject({
      trace_id: 'cam-1',
      window_ms: 1000,
      frames_drawn: 1,
      frames_drawn_fps: 1,
      scheduler_pending: 3,
    })
  })

  it('迟迟不出画时超时兜底上报 stalled，并说明数据包是否到达过解码器', () => {
    const harness = createHarness()
    harness.telemetry.attach(
      createDecoderStub({
        getFirstDecodeCallAtMs: () => null,
        getFirstSubmitAtMs: () => null,
      }).view
    )

    harness.advance(5000)

    expect(harness.coldStart).toHaveLength(1)
    expect(harness.coldStart[0].event).toBe('cold_start_render_stalled')
    expect(harness.coldStart[0].details).toMatchObject({
      outcome: 'stalled',
      decode_called: false,
      decode_submitted_ms: null,
      frame_output_ms: null,
      frame_drawn_ms: null,
    })

    // 一次渲染循环只交一次卷：超时之后不再上报 drawn
    harness.telemetry.markFirstDraw(6000)
    expect(harness.coldStart).toHaveLength(1)
  })

  it('稳态窗口在首次解码输出之后才开始，且窗口计数会归零', () => {
    const harness = createHarness()
    const decoder = createDecoderStub()
    harness.telemetry.attach(decoder.view)

    // 没出过画：不产出健康行，也不推进窗口
    harness.advance(3000)
    expect(harness.health).toHaveLength(0)

    // 出画后第一个采样只负责建立窗口起点（避免把冷启动等待算进帧率）
    decoder.setFirstOutput(1)
    harness.advance(1000)
    expect(harness.health).toHaveLength(0)

    harness.telemetry.countDrawnFrame()
    harness.telemetry.countPresentationDrop(2)
    harness.telemetry.countClockRebase()
    decoder.setSeqGap(5)
    harness.advance(1000)

    expect(harness.health).toHaveLength(1)
    expect(harness.health[0].details).toMatchObject({
      window_ms: 1000,
      frames_drawn: 1,
      presentation_drops: 2,
      clock_rebases: 1,
      seq_gap_frames: 5,
    })

    // 第二个窗口：计数已归零，seq 空洞按基线差值计算
    decoder.setSeqGap(7)
    harness.advance(1000)
    expect(harness.health).toHaveLength(2)
    expect(harness.health[1].details).toMatchObject({
      frames_drawn: 0,
      presentation_drops: 0,
      clock_rebases: 0,
      seq_gap_frames: 2,
    })
  })

  it('总闸关闭后两类日志都不产出', () => {
    const harness = createHarness({ disabled: true })
    expect(isPreviewTraceEnabled()).toBe(false)

    const decoder = createDecoderStub()
    decoder.setFirstOutput(1)
    harness.telemetry.attach(decoder.view)
    harness.telemetry.markFirstDraw(10)
    harness.advance(10000)

    expect(harness.coldStart).toHaveLength(0)
    expect(harness.health).toHaveLength(0)
  })

  it('dispose 后定时器不再触发（避免卸载后继续刷日志）', () => {
    const harness = createHarness()
    const decoder = createDecoderStub()
    decoder.setFirstOutput(1)
    harness.telemetry.attach(decoder.view)
    harness.telemetry.dispose()

    harness.advance(20000)

    expect(harness.coldStart).toHaveLength(0)
    expect(harness.health).toHaveLength(0)
  })
})
