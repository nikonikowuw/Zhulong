import { useCallback, useEffect, useRef, type RefObject } from 'react'
import { PresentationScheduler } from '../services/presentation-scheduler'
import { createRenderDiagnosticSession } from '../services/render-diagnostics'
import { createPreviewTelemetry } from '../services/render-telemetry'
import {
  isWebCodecsDiagnosticsEnabled,
  logWebCodecsDiagnostic,
  StreamDecoder,
} from '../services/webcodecs-decoder'

export interface UseWebCodecsRenderLoopOptions {
  /** 为 false 时不建解码器（例如环境不支持 WebCodecs）。 */
  enabled: boolean
  canvasRef: RefObject<HTMLCanvasElement | null>
  /** 返回 true 时丢弃新帧，保留当前画面（冻结画面）。 */
  isPaused?: () => boolean
  /**
   * 每帧绘制完成后调用，此时 canvas 尺寸已与帧同步。
   * 分辨率上报、叠加层重绘等按帧后处理都挂在这里。
   */
  onFrameDrawn?: (frame: VideoFrame, canvas: HTMLCanvasElement) => void
  onDecodeError?: (error: unknown) => void
  /**
   * 首个画面真正画到 canvas 时回调一次（每次重建渲染循环最多一次）。
   * 上层用它把「黑屏等待」换成明确的等待态 —— 起播前 RTSP 建连耗时 1~2 秒，
   * 期间 WebSocket 早已 connected，没有这个信号就只能干黑屏。
   */
  onFirstFrame?: () => void
  /**
   * 变化时重建解码器与渲染循环（如切换摄像头）。
   * 用单个字符串而非数组表达，避免在依赖数组里写展开语法。
   */
  restartKey?: string
  /**
   * 起播追踪里标识本播放器的名称（通常是 cameraId）。
   * 只用于日志，变化不会重建解码器。
   */
  traceId?: string
}

export interface UseWebCodecsRenderLoopResult {
  decoderRef: RefObject<StreamDecoder | null>
  /** 丢弃待渲染帧并重置解码器（切换码流时使用）。 */
  reset: () => void
}

/**
 * WebCodecs 解码 + 按呈现时钟排期的 rAF 渲染循环。
 *
 * 核心契约：**帧的显示时刻由 PTS 决定，不由到达时刻决定。**
 * 解码产出的帧先进 `PresentationScheduler` 排队，rAF 每拍只取「已到呈现时刻」的
 * 最新一帧来画；早到的帧安静等待，晚到的帧立即补上，突发时只丢真正过期的帧。
 * 详细取舍见 `services/presentation-scheduler.ts`。
 *
 * 抽成共享 hook 的原因：这套「解码 → 排期 → 绘制 → 关闭 VideoFrame」的生命周期
 * 原先在 LivePlayer 与 CameraFrameSurface 各写了一份，两处的清理顺序与丢帧策略极易走偏
 * （例如 canvas 已卸载时忘记 `frame.close()` 会泄漏解码帧）。
 */
export function useWebCodecsRenderLoop({
  enabled,
  canvasRef,
  isPaused,
  onFrameDrawn,
  onDecodeError,
  onFirstFrame,
  restartKey = '',
  traceId = 'unknown',
}: UseWebCodecsRenderLoopOptions): UseWebCodecsRenderLoopResult {
  const schedulerRef = useRef<PresentationScheduler<VideoFrame> | null>(null)
  const rafIdRef = useRef<number | null>(null)
  const lastRafRequestedAtRef = useRef<number | null>(null)
  const decoderRef = useRef<StreamDecoder | null>(null)
  const traceIdRef = useRef(traceId)
  useEffect(() => {
    traceIdRef.current = traceId
  }, [traceId])

  // 回调经 ref 读取：放进依赖数组会让解码器按帧重建
  const isPausedRef = useRef(isPaused)
  useEffect(() => {
    isPausedRef.current = isPaused
  }, [isPaused])
  const onFrameDrawnRef = useRef(onFrameDrawn)
  useEffect(() => {
    onFrameDrawnRef.current = onFrameDrawn
  }, [onFrameDrawn])
  const onDecodeErrorRef = useRef(onDecodeError)
  useEffect(() => {
    onDecodeErrorRef.current = onDecodeError
  }, [onDecodeError])
  const onFirstFrameRef = useRef(onFirstFrame)
  useEffect(() => {
    onFirstFrameRef.current = onFirstFrame
  }, [onFirstFrame])

  /** 释放所有待呈现帧。视频帧不关闭会持续占用解码器的 buffer group。 */
  const releaseScheduledFrames = useCallback(() => {
    const scheduler = schedulerRef.current
    if (!scheduler) return
    for (const frame of scheduler.clear()) frame.close()
  }, [])

  const reset = useCallback(() => {
    if (rafIdRef.current !== null) {
      cancelAnimationFrame(rafIdRef.current)
      rafIdRef.current = null
    }
    lastRafRequestedAtRef.current = null
    releaseScheduledFrames()
    decoderRef.current?.reset()
  }, [releaseScheduledFrames])

  useEffect(() => {
    if (!enabled) return

    const diagnostic = createRenderDiagnosticSession(logWebCodecsDiagnostic)
    const scheduler = new PresentationScheduler<VideoFrame>()
    schedulerRef.current = scheduler
    // 起播/稳态观测全交给遥测采集器：它与排期、显存释放无耦合，可独立单测。
    const telemetry = createPreviewTelemetry({
      traceId: () => traceIdRef.current,
      schedulerPending: () => scheduler.pending,
    })

    function requestRender(): void {
      if (rafIdRef.current !== null) return
      diagnostic.count('rafRequests')
      lastRafRequestedAtRef.current = isWebCodecsDiagnosticsEnabled()
        ? performance.now()
        : null
      rafIdRef.current = requestAnimationFrame(renderLoop)
    }

    function drawFrame(frame: VideoFrame): void {
      const canvas = canvasRef.current
      if (!canvas) {
        diagnostic.count('framesWithoutCanvas')
        // canvas 已卸载：必须关闭，否则解码帧泄漏
        frame.close()
        return
      }

      if (
        canvas.width !== frame.displayWidth ||
        canvas.height !== frame.displayHeight
      ) {
        canvas.width = frame.displayWidth
        canvas.height = frame.displayHeight
      }

      const ctx = canvas.getContext('2d')
      if (ctx) {
        const drawStartedAt = diagnostic.now()
        try {
          ctx.drawImage(frame, 0, 0, canvas.width, canvas.height)
          if (drawStartedAt > 0) {
            diagnostic.recordDraw(
              performance.now() - drawStartedAt,
              drawStartedAt
            )
          }
        } catch (error) {
          diagnostic.count('drawErrors')
          frame.close()
          if (isWebCodecsDiagnosticsEnabled()) {
            logWebCodecsDiagnostic('error', 'canvas_draw_failed', {
              errorName: error instanceof Error ? error.name : 'UnknownError',
              errorMessage:
                error instanceof Error ? error.message : String(error),
              frameSize: `${frame.displayWidth}x${frame.displayHeight}`,
              canvasSize: `${canvas.width}x${canvas.height}`,
            })
          }
          throw error
        }
      } else {
        diagnostic.count('framesWithoutContext')
      }

      onFrameDrawnRef.current?.(frame, canvas)
      diagnostic.count('frameCallbacks')
      frame.close()

      telemetry.countDrawnFrame()
      const firstDraw = telemetry.markFirstDraw(performance.now())
      if (firstDraw) onFirstFrameRef.current?.()
    }

    /** 记一次呈现时钟重锚（源流时间轴跳变）。 */
    function countClockRebase(): void {
      diagnostic.count('clockRebases')
      telemetry.countClockRebase()
    }

    /** 释放一帧并记一次呈现丢帧（错过呈现窗口 / 队列裁剪）。 */
    function dropFrame(frame: VideoFrame): void {
      diagnostic.count('presentationDrops')
      telemetry.countPresentationDrop()
      frame.close()
    }

    function renderLoop(timestamp: number): void {
      diagnostic.recordRafCallback(timestamp)
      if (!isWebCodecsDiagnosticsEnabled()) {
        lastRafRequestedAtRef.current = null
      } else if (lastRafRequestedAtRef.current !== null) {
        diagnostic.recordRafDelay(
          Math.max(0, timestamp - lastRafRequestedAtRef.current)
        )
        lastRafRequestedAtRef.current = null
      }
      rafIdRef.current = null

      // rAF timestamp 与 performance.now() 同源，但排期一律用后者，
      // 避免两条时钟在诊断与调度之间产生毫秒级口径差。
      const presentation = scheduler.takeDue(performance.now())
      if (presentation.rebased) countClockRebase()
      for (const stale of presentation.expired) {
        dropFrame(stale)
      }

      const frame = presentation.present
      if (frame) {
        drawFrame(frame)
      } else if (presentation.pending === 0) {
        // 队列空且无帧可画：只可能是刚被 reset，属正常空转
        diagnostic.count('rafWithoutFrame')
      }

      // 队列里仍有未到呈现时刻的帧：继续按 vsync 排期等待，而不是等下一帧到达才醒
      if (presentation.pending > 0) requestRender()
    }

    const decoder = new StreamDecoder({
      onFrame: (frame: VideoFrame) => {
        diagnostic.recordOutputFrame(frame, canvasRef.current)
        decoder.markFirstOutputAt(performance.now())
        if (isPausedRef.current?.()) {
          diagnostic.count('pausedDrops')
          frame.close()
          return
        }
        const pushed = scheduler.push(frame.timestamp, frame, performance.now())
        if (pushed.rebased) countClockRebase()
        for (const dropped of pushed.dropped) {
          dropFrame(dropped)
        }
        requestRender()
      },
      onError: (error: unknown) => {
        onDecodeErrorRef.current?.(error)
      },
    })
    decoderRef.current = decoder
    telemetry.attach(decoder)

    return () => {
      telemetry.dispose()
      if (rafIdRef.current !== null) {
        cancelAnimationFrame(rafIdRef.current)
        rafIdRef.current = null
      }
      lastRafRequestedAtRef.current = null
      schedulerRef.current = null
      for (const frame of scheduler.clear()) frame.close()
      decoder.close()
      decoderRef.current = null
    }
  }, [enabled, restartKey, canvasRef, reset])

  return { decoderRef, reset }
}
