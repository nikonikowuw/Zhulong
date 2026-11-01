import { boxesCoincide } from '@/lib/box-geometry'
import { type PersonItem } from '../types/ai-result'

export interface DrawPersonOverlayOptions {
  ctx: CanvasRenderingContext2D
  width: number
  height: number
  persons: PersonItem[]
  personLabel?: string
  clear?: boolean
}

/**
 * 判断是否「纯人脸退化项」：没有独立人体检出时，插件把 `body_box` 原样别名成
 * `faces[0].box`（见 native/sdk/include/zhulong/plugin.h 退化语义）。
 * 这类项必须跳过人体框绘制，否则近景会给同一张脸叠画两个框。
 *
 * ⚠️ 不要用 `track_id === 0` 当判据：未接入跟踪器时，真人体项的 `track_id`
 * 同样是 0（见 packages/face_recognition 的 pipeline），几何重合才是可靠信号。
 */
export function isFaceCloseupPerson(person: PersonItem): boolean {
  if (person.faces.length !== 1) {
    return false
  }
  return boxesCoincide(person.body_box, person.faces[0].box)
}

/**
 * 在 Canvas 上绘制人体框叠加层 (PersonOverlayRenderer)。
 *
 * 视觉层级低于人脸框：细实线紫框 + 框内左上角标签，不画四角 HUD。
 * 人脸框（翠绿/琥珀 + HUD 角标 + 外挂 badge）始终压在人体框之上，
 * 车牌框（蓝/绿/黄）也不会与紫色混淆。
 */
export function drawPersonOverlay({
  ctx,
  width,
  height,
  persons,
  personLabel = '人体',
  clear = true,
}: DrawPersonOverlayOptions): void {
  if (clear) {
    ctx.clearRect(0, 0, width, height)
  }

  if (!persons || persons.length === 0 || width <= 0 || height <= 0) {
    return
  }

  const lineWidth = Math.max(1.5, width / 900)
  const fontSize = Math.max(10, Math.min(16, Math.round(width / 90)))
  const paddingX = 5

  for (const person of persons) {
    if (isFaceCloseupPerson(person)) {
      continue
    }

    const [rawX1, rawY1, rawX2, rawY2] = person.body_box
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

    ctx.setLineDash([])
    ctx.strokeStyle = '#a855f7'
    ctx.lineWidth = lineWidth
    ctx.strokeRect(bx, by, bw, bh)

    // 标签内嵌在框内左上角，避开人脸框上方外挂的识别 badge。
    // 框太小放不下标签时只画框：把文字强行塞进小框只会留下被裁掉半截的残字。
    const scorePercent = Math.round(
      Math.max(0, Math.min(1, person.body_score)) * 100
    )
    const trackText = person.track_id > 0 ? ` #${person.track_id}` : ''
    // 插件未填 body_score 时不要显示「0%」——那是缺分数，不是低置信度。
    const scoreText = scorePercent > 0 ? ` ${scorePercent}%` : ''
    const labelText = `${personLabel}${trackText}${scoreText}`

    ctx.font = `600 ${fontSize}px system-ui, -apple-system, sans-serif`
    ctx.textBaseline = 'middle'

    const metrics = ctx.measureText(labelText)
    const badgeHeight = fontSize + 6
    const badgeWidth = metrics.width + paddingX * 2
    const canDrawLabel = bh >= badgeHeight && badgeWidth <= width

    if (canDrawLabel) {
      const badgeX = Math.min(Math.max(bx, 0), Math.max(0, width - badgeWidth))

      ctx.setLineDash([])
      ctx.fillStyle = 'rgba(168, 85, 247, 0.88)'
      if (typeof ctx.roundRect === 'function') {
        ctx.beginPath()
        ctx.roundRect(badgeX, by, badgeWidth, badgeHeight, 3)
        ctx.fill()
      } else {
        ctx.fillRect(badgeX, by, badgeWidth, badgeHeight)
      }

      ctx.fillStyle = '#ffffff'
      ctx.fillText(labelText, badgeX + paddingX, by + badgeHeight / 2)
    }

    ctx.restore()
  }
}
