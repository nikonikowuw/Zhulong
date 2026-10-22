import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { AIResultService } from '../services/ai-result-service'
import { drawVehicleOverlay } from '../services/vehicle-overlay-renderer'
import { type VehicleItem } from '../types/ai-result'

export interface LiveVehicleOverlayProps {
  cameraId?: string | null
  enabled?: boolean
  className?: string
}

/**
 * 独立机动车与车牌检测/识别 Canvas 叠加层组件 (LiveVehicleOverlay)
 * 通过 AIResultService 订阅原生推理结果中的 vehicles，基于 rAF 驱动 Canvas 2D 绘制，
 * 避免逐帧触发 React 重渲染，且支持 350ms 自动消退防残留。
 */
export function LiveVehicleOverlay({
  cameraId,
  enabled = true,
  className = '',
}: LiveVehicleOverlayProps): React.JSX.Element {
  const { t } = useTranslation('live')
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const containerRef = useRef<HTMLDivElement | null>(null)
  const vehiclesRef = useRef<VehicleItem[]>([])
  const lastEventTimeRef = useRef<number>(0)
  const fadeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    const canvas = canvasRef.current
    const container = containerRef.current
    if (!canvas || !container) return

    // 监听容器几何尺寸，自适应更新 Canvas 像素分辨率
    const updateSize = () => {
      const rect = container.getBoundingClientRect()
      const dpr = window.devicePixelRatio || 1
      const w = Math.round(rect.width * dpr)
      const h = Math.round(rect.height * dpr)
      if (w > 0 && h > 0 && (canvas.width !== w || canvas.height !== h)) {
        canvas.width = w
        canvas.height = h
        redraw()
      }
    }

    const redraw = () => {
      const currentCanvas = canvasRef.current
      if (!currentCanvas || currentCanvas.width === 0) return
      const ctx = currentCanvas.getContext('2d')
      if (!ctx) return

      const isExpired = Date.now() - lastEventTimeRef.current > 350
      drawVehicleOverlay({
        ctx,
        width: currentCanvas.width,
        height: currentCanvas.height,
        vehicles: isExpired ? [] : vehiclesRef.current,
        unrecognizedLabel: t('unrecognizedPlate', '未识别车牌'),
        clear: true,
      })
    }

    updateSize()
    const resizeObserver = new ResizeObserver(() => {
      requestAnimationFrame(updateSize)
    })
    resizeObserver.observe(container)

    if (!enabled || !cameraId) {
      vehiclesRef.current = []
      redraw()
      return () => {
        resizeObserver.disconnect()
      }
    }

    const service = AIResultService.getInstance()
    const unsubscribe = service.subscribeCamera(cameraId, (event) => {
      const extractedVehicles: VehicleItem[] = []
      if (event.vehicles && event.vehicles.length > 0) {
        extractedVehicles.push(...event.vehicles)
      }

      vehiclesRef.current = extractedVehicles
      lastEventTimeRef.current = Date.now()
      redraw()

      if (fadeTimerRef.current) {
        clearTimeout(fadeTimerRef.current)
      }
      fadeTimerRef.current = setTimeout(() => {
        vehiclesRef.current = []
        redraw()
        fadeTimerRef.current = null
      }, 350)
    })

    return () => {
      if (fadeTimerRef.current) {
        clearTimeout(fadeTimerRef.current)
        fadeTimerRef.current = null
      }
      unsubscribe()
      resizeObserver.disconnect()
      vehiclesRef.current = []
      redraw()
    }
  }, [cameraId, enabled, t])

  if (!enabled) {
    return <></>
  }

  return (
    <div
      ref={containerRef}
      className={`pointer-events-none absolute inset-0 h-full w-full overflow-hidden ${className}`}
    >
      <canvas ref={canvasRef} className='h-full w-full' />
    </div>
  )
}
