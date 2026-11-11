import { type DetectionItem } from '../types/ai-result'

export interface DrawDetectionOverlayOptions {
  ctx: CanvasRenderingContext2D
  width: number
  height: number
  detections: DetectionItem[]
  fallbackLabel?: string
  clear?: boolean
}

/**
 * 在 Canvas 上绘制通用目标检测叠加层 (DetectionOverlayRenderer)。
 *
 * 采用青色（Cyan `#06b6d4`）视觉语言，与人体紫框、人脸翠绿/琥珀框及车牌蓝/黄/绿框清晰区分。
 */
export function drawDetectionOverlay({
  ctx,
  width,
  height,
  detections,
  fallbackLabel = '目标',
  clear = true,
}: DrawDetectionOverlayOptions): void {
  if (clear) {
    ctx.clearRect(0, 0, width, height)
  }

  if (!detections || detections.length === 0 || width <= 0 || height <= 0) {
    return
  }

  const lineWidth = Math.max(1.75, width / 720)
  const cornerLength = Math.max(7, Math.round(width / 110))
  const fontSize = Math.max(10, Math.min(16, Math.round(width / 88)))
  const paddingX = 6

  for (const det of detections) {
    const [rawX1, rawY1, rawX2, rawY2] = det.box
    const x1 = Math.max(0, Math.min(1, rawX1))
    const y1 = Math.max(0, Math.min(1, rawY1))
    const x2 = Math.max(x1, Math.min(1, rawX2))
    const y2 = Math.max(y1, Math.min(1, rawY2))

    const bx = Math.round(x1 * width)
    const by = Math.round(y1 * height)
    const bw = Math.round((x2 - x1) * width)
    const bh = Math.round((y2 - y1) * height)

    if (bw <= 0 || bh <= 0) {
      continue
    }

    ctx.save()

    ctx.fillStyle = 'rgba(6, 182, 212, 0.08)'
    ctx.fillRect(bx, by, bw, bh)

    ctx.setLineDash([])
    ctx.strokeStyle = '#06b6d4'
    ctx.lineWidth = lineWidth
    ctx.strokeRect(bx, by, bw, bh)

    if (bw > cornerLength * 2 && bh > cornerLength * 2) {
      ctx.lineWidth = lineWidth + 1.25
      ctx.strokeStyle = '#22d3ee'
      const cl = Math.min(cornerLength, Math.floor(Math.min(bw, bh) / 3))

      ctx.beginPath()
      ctx.moveTo(bx, by + cl)
      ctx.lineTo(bx, by)
      ctx.lineTo(bx + cl, by)
      ctx.stroke()

      ctx.beginPath()
      ctx.moveTo(bx + bw - cl, by)
      ctx.lineTo(bx + bw, by)
      ctx.lineTo(bx + bw, by + cl)
      ctx.stroke()

      ctx.beginPath()
      ctx.moveTo(bx, by + bh - cl)
      ctx.lineTo(bx, by + bh)
      ctx.lineTo(bx + cl, by + bh)
      ctx.stroke()

      ctx.beginPath()
      ctx.moveTo(bx + bw - cl, by + bh)
      ctx.lineTo(bx + bw, by + bh)
      ctx.lineTo(bx + bw, by + bh - cl)
      ctx.stroke()
    }

    const scorePercent = Math.round(Math.max(0, Math.min(1, det.score)) * 100)
    const rawName = det.label.trim() || fallbackLabel
    const labelText =
      scorePercent > 0 ? `${rawName} (${scorePercent}%)` : rawName

    ctx.font = `600 ${fontSize}px system-ui, -apple-system, sans-serif`
    ctx.textBaseline = 'middle'

    const metrics = ctx.measureText(labelText)
    const badgeHeight = fontSize + 6
    const badgeWidth = metrics.width + paddingX * 2

    if (badgeWidth <= width) {
      let badgeX = bx
      if (badgeX + badgeWidth > width) {
        badgeX = Math.max(0, width - badgeWidth - 2)
      }
      let badgeY = by - badgeHeight - 2
      if (badgeY < 0) {
        badgeY = by + 2
      }

      ctx.fillStyle = 'rgba(8, 145, 178, 0.90)'
      if (typeof ctx.roundRect === 'function') {
        ctx.beginPath()
        ctx.roundRect(badgeX, badgeY, badgeWidth, badgeHeight, 3)
        ctx.fill()
      } else {
        ctx.fillRect(badgeX, badgeY, badgeWidth, badgeHeight)
      }

      ctx.fillStyle = '#ffffff'
      ctx.fillText(labelText, badgeX + paddingX, badgeY + badgeHeight / 2)
    }

    ctx.restore()
  }
}
