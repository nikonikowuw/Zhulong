import { useTranslation } from 'react-i18next'
import { useAIOverlayCanvas } from '../hooks/use-ai-overlay-canvas'
import { drawPersonOverlay } from '../services/person-overlay-renderer'
import { type PersonItem } from '../types/ai-result'

export interface LivePersonOverlayProps {
  cameraId?: string | null
  enabled?: boolean
  className?: string
}

/**
 * 独立人体框 Canvas 叠加层组件 (LivePersonOverlay)
 * 通过 useAIOverlayCanvas 订阅原生推理结果中的 persons，绘制真实人体框；
 * 纯人脸退化项（body_box 别名为人脸框）由 renderer 内部跳过，不会与脸框重叠。
 * 挂载顺序须位于人脸/车辆叠加层之前，保证小目标的标签压在最上层。
 */
export function LivePersonOverlay({
  cameraId,
  enabled = true,
  className = '',
}: LivePersonOverlayProps): React.JSX.Element {
  const { t } = useTranslation('live')

  const { containerRef, canvasRef } = useAIOverlayCanvas<PersonItem[]>({
    cameraId,
    enabled,
    extract: (event) => event.persons,
    draw: (ctx, size, persons) => {
      drawPersonOverlay({
        ctx,
        width: size.width,
        height: size.height,
        persons: persons ?? [],
        personLabel: t('personLabel', '人体'),
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
