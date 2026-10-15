import { type FaceSubItem } from '../types/ai-result'

export interface DrawFaceOverlayOptions {
  ctx: CanvasRenderingContext2D
  width: number
  height: number
  faces: FaceSubItem[]
  unrecognizedLabel?: string
}

/**
 * 在 Canvas 上绘制低延迟、高保真的人脸检测/识别叠加层 (FaceOverlayRenderer)
 * 采用归一化坐标自适应映射，支持命中高亮、未登记提示、置信度标牌与5点关键点。
 */
export function drawFaceOverlay({
  ctx,
  width,
  height,
  faces,
  unrecognizedLabel = '未识别',
}: DrawFaceOverlayOptions): void {
  ctx.clearRect(0, 0, width, height)

  if (!faces || faces.length === 0 || width <= 0 || height <= 0) {
    return
  }

  const baseLineWidth = Math.max(2, Math.round(width / 640))
  const cornerLength = Math.max(8, Math.round(width / 100))
  const fontSize = Math.max(11, Math.min(18, Math.round(width / 80)))

  for (const face of faces) {
    const [rawX1, rawY1, rawX2, rawY2] = face.box
    const x1 = Math.max(0, Math.min(1, rawX1))
    const y1 = Math.max(0, Math.min(1, rawY1))
    const x2 = Math.max(x1, Math.min(1, rawX2))
    const y2 = Math.max(y1, Math.min(1, rawY2))

    const bx = Math.round(x1 * width)
    const by = Math.round(y1 * height)
    const bw = Math.round((x2 - x1) * width)
    const bh = Math.round((y2 - y1) * height)

    if (bw <= 0 || bh <= 0) continue

    ctx.save()

    const strokeColor = face.matched ? '#10b981' : '#f59e0b'
    const fillColor = face.matched
      ? 'rgba(16, 185, 129, 0.08)'
      : 'rgba(245, 158, 11, 0.08)'
    const badgeBg = face.matched
      ? 'rgba(16, 185, 129, 0.90)'
      : 'rgba(245, 158, 11, 0.90)'

    // 1. 矩形半透明背景与边框
    ctx.fillStyle = fillColor
    ctx.fillRect(bx, by, bw, bh)

    ctx.strokeStyle = strokeColor
    ctx.lineWidth = baseLineWidth
    if (!face.matched) {
      ctx.setLineDash([4, 4])
    } else {
      ctx.setLineDash([])
    }
    ctx.strokeRect(bx, by, bw, bh)

    // 2. 四角 HUD 强化视觉（仅在尺寸足够时绘制）
    if (bw > cornerLength * 2 && bh > cornerLength * 2) {
      ctx.setLineDash([])
      ctx.lineWidth = baseLineWidth + 1.5
      ctx.strokeStyle = strokeColor
      const cl = Math.min(cornerLength, Math.floor(Math.min(bw, bh) / 3))

      // 左上
      ctx.beginPath()
      ctx.moveTo(bx, by + cl)
      ctx.lineTo(bx, by)
      ctx.lineTo(bx + cl, by)
      ctx.stroke()

      // 右上
      ctx.beginPath()
      ctx.moveTo(bx + bw - cl, by)
      ctx.lineTo(bx + bw, by)
      ctx.lineTo(bx + bw, by + cl)
      ctx.stroke()

      // 左下
      ctx.beginPath()
      ctx.moveTo(bx, by + bh - cl)
      ctx.lineTo(bx, by + bh)
      ctx.lineTo(bx + cl, by + bh)
      ctx.stroke()

      // 右下
      ctx.beginPath()
      ctx.moveTo(bx + bw - cl, by + bh)
      ctx.lineTo(bx + bw, by + bh)
      ctx.lineTo(bx + bw, by + bh - cl)
      ctx.stroke()
    }

    // 3. 5点关键点 (Landmarks)
    if (face.landmarks && face.landmarks.length > 0) {
      const pointRadius = Math.max(2, Math.round(width / 600))
      for (const [lx, ly] of face.landmarks) {
        if (lx >= 0 && lx <= 1 && ly >= 0 && ly <= 1) {
          const px = Math.round(lx * width)
          const py = Math.round(ly * height)

          ctx.beginPath()
          ctx.arc(px, py, pointRadius, 0, Math.PI * 2)
          ctx.fillStyle = face.matched ? '#34d399' : '#fde047'
          ctx.fill()
          ctx.strokeStyle = 'rgba(0, 0, 0, 0.6)'
          ctx.lineWidth = 1
          ctx.stroke()
        }
      }
    }

    // 4. 识别标牌 (Badge)
    ctx.setLineDash([])
    ctx.font = `bold ${fontSize}px system-ui, -apple-system, sans-serif`
    ctx.textBaseline = 'middle'

    const simPercent = Math.round(
      Math.max(0, Math.min(1, face.similarity)) * 100
    )
    const labelText = face.matched
      ? `${face.person_name || face.person_id} (${simPercent}%)`
      : `${unrecognizedLabel} (${simPercent}%)`

    const metrics = ctx.measureText(labelText)
    const badgePaddingX = 6
    const badgeHeight = fontSize + 8
    const badgeWidth = metrics.width + badgePaddingX * 2

    let badgeX = bx
    if (badgeX + badgeWidth > width) {
      badgeX = Math.max(0, width - badgeWidth - 2)
    }

    let badgeY = by - badgeHeight - 2
    if (badgeY < 0) {
      badgeY = by + 2
    }

    ctx.fillStyle = badgeBg
    if (typeof ctx.roundRect === 'function') {
      ctx.beginPath()
      ctx.roundRect(badgeX, badgeY, badgeWidth, badgeHeight, 3)
      ctx.fill()
    } else {
      ctx.fillRect(badgeX, badgeY, badgeWidth, badgeHeight)
    }

    ctx.fillStyle = '#ffffff'
    ctx.fillText(labelText, badgeX + badgePaddingX, badgeY + badgeHeight / 2)

    ctx.restore()
  }
}
