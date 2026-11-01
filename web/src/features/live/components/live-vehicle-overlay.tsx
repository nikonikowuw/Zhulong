import { useTranslation } from 'react-i18next'
import { useAIOverlayCanvas } from '../hooks/use-ai-overlay-canvas'
import { drawVehicleOverlay } from '../services/vehicle-overlay-renderer'
import { type VehicleItem } from '../types/ai-result'

export interface LiveVehicleOverlayProps {
  cameraId?: string | null
  enabled?: boolean
  className?: string
}

/**
 * 独立机动车与车牌检测/识别 Canvas 叠加层组件 (LiveVehicleOverlay)
 * 通过 useAIOverlayCanvas 订阅原生推理结果中的 vehicles，基于 Canvas 2D 直接绘制，
 * 避免逐帧触发 React 重渲染，且支持 350ms 自动消退防残留。
 */
export function LiveVehicleOverlay({
  cameraId,
  enabled = true,
  className = '',
}: LiveVehicleOverlayProps): React.JSX.Element {
  const { t } = useTranslation('live')

  const { containerRef, canvasRef } = useAIOverlayCanvas<VehicleItem[]>({
    cameraId,
    enabled,
    extract: (event) => event.vehicles,
    draw: (ctx, size, vehicles) => {
      drawVehicleOverlay({
        ctx,
        width: size.width,
        height: size.height,
        vehicles: vehicles ?? [],
        unrecognizedLabel: t('unrecognizedPlate', '未识别车牌'),
        clear: true,
      })
    },
  })

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
