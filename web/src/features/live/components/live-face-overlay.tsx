import { useTranslation } from 'react-i18next'
import { useAIOverlayCanvas } from '../hooks/use-ai-overlay-canvas'
import { drawFaceOverlay } from '../services/face-overlay-renderer'
import { type FaceSubItem } from '../types/ai-result'

export interface LiveFaceOverlayProps {
  cameraId?: string | null
  enabled?: boolean
  className?: string
}

/**
 * 独立人脸检测与识别 Canvas 叠加层组件 (LiveFaceOverlay)
 * 通过 useAIOverlayCanvas 订阅原生推理结果，基于 Canvas 2D 直接绘制，
 * 避免逐帧触发 React 重渲染，且支持 350ms 自动消退防残留。
 */
export function LiveFaceOverlay({
  cameraId,
  enabled = true,
  className = '',
}: LiveFaceOverlayProps): React.JSX.Element {
  const { t } = useTranslation('live')

  const { containerRef, canvasRef } = useAIOverlayCanvas<FaceSubItem[]>({
    cameraId,
    enabled,
    extract: (event) =>
      (event.persons ?? []).flatMap((person) => person.faces ?? []),
    draw: (ctx, size, faces) => {
      drawFaceOverlay({
        ctx,
        width: size.width,
        height: size.height,
        faces: faces ?? [],
        unrecognizedLabel: t('unrecognizedFace', '未登记'),
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
