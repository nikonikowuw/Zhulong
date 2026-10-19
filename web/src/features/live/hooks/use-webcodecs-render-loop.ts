import { useCallback, useEffect, useRef, type RefObject } from 'react'
import { StreamDecoder } from '../services/webcodecs-decoder'

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
   * 变化时重建解码器与渲染循环（如人脸叠加层开关、切换摄像头）。
   * 用单个字符串而非数组表达，避免在依赖数组里写展开语法。
   */
  restartKey?: string
}

export interface UseWebCodecsRenderLoopResult {
  decoderRef: RefObject<StreamDecoder | null>
  /** 丢弃待渲染帧并重置解码器（切换码流时使用）。 */
  reset: () => void
}

/**
 * WebCodecs 解码 + requestAnimationFrame 渲染循环。
 *
 * 抽成共享 hook 的原因：这套「缓冲最新帧 → rAF 里绘制 → 关闭 VideoFrame」的生命周期
 * 原先在 LivePlayer 与 CameraFrameSurface 各写了一份，两处的清理顺序与丢帧策略极易走偏
 * （例如 canvas 已卸载时忘记 `frame.close()` 会泄漏解码帧）。
 */
export function useWebCodecsRenderLoop({
  enabled,
  canvasRef,
  isPaused,
  onFrameDrawn,
  onDecodeError,
  restartKey = '',
}: UseWebCodecsRenderLoopOptions): UseWebCodecsRenderLoopResult {
  const pendingFrameRef = useRef<VideoFrame | null>(null)
  const rafIdRef = useRef<number | null>(null)
  const decoderRef = useRef<StreamDecoder | null>(null)

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

  const reset = useCallback(() => {
    if (rafIdRef.current !== null) {
      cancelAnimationFrame(rafIdRef.current)
      rafIdRef.current = null
    }
    if (pendingFrameRef.current) {
      pendingFrameRef.current.close()
      pendingFrameRef.current = null
    }
    decoderRef.current?.reset()
  }, [])

  useEffect(() => {
    if (!enabled) return

    const renderLoop = () => {
      const frame = pendingFrameRef.current
      pendingFrameRef.current = null
      rafIdRef.current = null
      if (!frame) return

      const canvas = canvasRef.current
      if (!canvas) {
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
        ctx.drawImage(frame, 0, 0, canvas.width, canvas.height)
      }

      onFrameDrawnRef.current?.(frame, canvas)
      frame.close()
    }

    const decoder = new StreamDecoder({
      onFrame: (frame: VideoFrame) => {
        if (isPausedRef.current?.()) {
          frame.close()
          return
        }
        if (pendingFrameRef.current) {
          pendingFrameRef.current.close()
        }
        pendingFrameRef.current = frame
        if (rafIdRef.current === null) {
          rafIdRef.current = requestAnimationFrame(renderLoop)
        }
      },
      onError: (error: unknown) => {
        onDecodeErrorRef.current?.(error)
      },
    })
    decoderRef.current = decoder

    return () => {
      if (rafIdRef.current !== null) {
        cancelAnimationFrame(rafIdRef.current)
        rafIdRef.current = null
      }
      if (pendingFrameRef.current) {
        pendingFrameRef.current.close()
        pendingFrameRef.current = null
      }
      decoder.close()
      decoderRef.current = null
    }
  }, [enabled, restartKey, canvasRef, reset])

  return { decoderRef, reset }
}
