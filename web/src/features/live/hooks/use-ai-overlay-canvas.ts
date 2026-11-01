import { useEffect, useRef, type RefObject } from 'react'
import { AIResultService } from '../services/ai-result-service'
import { type AIResultEvent } from '../types/ai-result'

export interface AIOverlayCanvasSize {
  width: number
  height: number
}

export interface UseAIOverlayCanvasOptions<T> {
  cameraId?: string | null
  enabled: boolean
  /** 从一次 AI 结果事件中提取本叠加层要绘制的数据。 */
  extract: (event: AIResultEvent) => T
  /**
   * 绘制回调。`data` 为 null 表示当前没有有效结果（消退或未订阅），
   * 实现应清空画布，避免残留幽灵框。
   */
  draw: (
    ctx: CanvasRenderingContext2D,
    size: AIOverlayCanvasSize,
    data: T | null
  ) => void
  /** 无新结果多久后消退，默认 350ms。 */
  fadeMs?: number
}

/**
 * AI 感知叠加层的画布生命周期 Hook（零 React 重渲染）。
 *
 * 负责三件仅与 canvas 有关的事：容器几何 → 画布像素分辨率同步、
 * `AIResultService` 事件订阅、以及无结果时的消退计时。
 * 具体画什么由调用方的 `draw` 决定；`extract`/`draw` 经 ref 保存，
 * 内联闭包不会导致重新订阅。
 *
 * ⚠️ 高频叠加层禁止改用 `useState` 承载结果（见 use-ai-results.ts 的约束）。
 */
export function useAIOverlayCanvas<T>({
  cameraId,
  enabled,
  extract,
  draw,
  fadeMs = 350,
}: UseAIOverlayCanvasOptions<T>): {
  containerRef: RefObject<HTMLDivElement | null>
  canvasRef: RefObject<HTMLCanvasElement | null>
} {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const dataRef = useRef<T | null>(null)
  const lastEventAtRef = useRef<number>(0)
  const fadeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const extractRef = useRef(extract)
  const drawRef = useRef(draw)

  // 内联闭包每次 render 都会变，用 effect 同步而非在 render 期间写 ref。
  useEffect(() => {
    extractRef.current = extract
    drawRef.current = draw
  }, [extract, draw])

  useEffect(() => {
    const canvas = canvasRef.current
    const container = containerRef.current
    if (!canvas || !container) return

    const redraw = () => {
      const current = canvasRef.current
      if (!current || current.width === 0 || current.height === 0) return
      const ctx = current.getContext('2d')
      if (!ctx) return

      const isExpired = Date.now() - lastEventAtRef.current > fadeMs
      drawRef.current(
        ctx,
        { width: current.width, height: current.height },
        isExpired ? null : dataRef.current
      )
    }

    const updateSize = () => {
      const current = canvasRef.current
      if (!current) return
      const rect = container.getBoundingClientRect()
      const dpr = window.devicePixelRatio || 1
      const w = Math.round(rect.width * dpr)
      const h = Math.round(rect.height * dpr)
      if (w > 0 && h > 0 && (current.width !== w || current.height !== h)) {
        current.width = w
        current.height = h
        redraw()
      }
    }

    updateSize()
    const resizeObserver = new ResizeObserver(() => {
      requestAnimationFrame(updateSize)
    })
    resizeObserver.observe(container)

    if (!enabled || !cameraId) {
      dataRef.current = null
      redraw()
      return () => {
        resizeObserver.disconnect()
      }
    }

    const unsubscribe = AIResultService.getInstance().subscribeCamera(
      cameraId,
      (event) => {
        dataRef.current = extractRef.current(event)
        lastEventAtRef.current = Date.now()
        redraw()

        if (fadeTimerRef.current) {
          clearTimeout(fadeTimerRef.current)
        }
        fadeTimerRef.current = setTimeout(() => {
          fadeTimerRef.current = null
          dataRef.current = null
          redraw()
        }, fadeMs)
      }
    )

    return () => {
      if (fadeTimerRef.current) {
        clearTimeout(fadeTimerRef.current)
        fadeTimerRef.current = null
      }
      unsubscribe()
      resizeObserver.disconnect()
      dataRef.current = null
      redraw()
    }
  }, [cameraId, enabled, fadeMs])

  return { containerRef, canvasRef }
}
