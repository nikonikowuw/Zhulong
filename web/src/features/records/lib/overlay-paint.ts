import { type BoundingBox, type LandmarkPoint } from '../data/schema'
import { isValidBox, toAbsoluteCoords } from './media-fetch'

/**
 * 感知记录图片烧录的**纯绘制核**。
 *
 * 这里只依赖 Canvas 2D 上下文，不碰 DOM、不发请求、不读 i18n：主线程的
 * `canvas-overlay-drawer.ts` 与导出 Worker（`export-image.worker.ts`）共用同一份
 * 绘制逻辑，避免两条路径画出不一样的框。
 */

export type PaletteKey = 'emerald' | 'rose' | 'amber' | 'sky' | 'violet'

/** 一次烧录的纯数据描述（可结构化克隆，可直接进 Worker） */
export interface OverlayPaintOptions {
  /** 归一化目标框 [x1, y1, x2, y2] 或物理像素坐标框 */
  primaryBox?: BoundingBox
  primaryLabel?: string
  /** 归一化人体框 (识别记录特有) */
  secondaryBox?: BoundingBox
  secondaryLabel?: string
  /** 主要框颜色风格 */
  palette?: PaletteKey
  /** 次要框颜色风格，默认 'violet' */
  secondaryPalette?: PaletteKey
  /** 人脸 5 点关键点（识别记录特有） */
  landmarks?: LandmarkPoint[]
}

const PALETTE_CONFIG: Record<
  PaletteKey,
  {
    stroke: string
    fill: string
    badgeBg: string
    badgeText: string
  }
> = {
  emerald: {
    stroke: '#10b981',
    fill: 'rgba(16, 185, 129, 0.15)',
    badgeBg: 'rgba(5, 150, 105, 0.95)',
    badgeText: '#ffffff',
  },
  rose: {
    stroke: '#f43f5e',
    fill: 'rgba(244, 63, 94, 0.15)',
    badgeBg: 'rgba(225, 29, 72, 0.95)',
    badgeText: '#ffffff',
  },
  amber: {
    stroke: '#f59e0b',
    fill: 'rgba(245, 158, 11, 0.15)',
    badgeBg: 'rgba(217, 119, 6, 0.95)',
    badgeText: '#ffffff',
  },
  sky: {
    stroke: '#0ea5e9',
    fill: 'rgba(14, 165, 233, 0.15)',
    badgeBg: 'rgba(2, 132, 199, 0.95)',
    badgeText: '#ffffff',
  },
  violet: {
    stroke: '#8b5cf6',
    fill: 'rgba(139, 92, 246, 0.15)',
    badgeBg: 'rgba(124, 58, 237, 0.95)',
    badgeText: '#ffffff',
  },
}

type OverlayContext =
  | CanvasRenderingContext2D
  | OffscreenCanvasRenderingContext2D

function drawBoxAndLabel(
  ctx: OverlayContext,
  rect: { x: number; y: number; width: number; height: number },
  label: string | undefined,
  palette: PaletteKey,
  canvasWidth: number,
  isDashed = false
) {
  const colors = PALETTE_CONFIG[palette] || PALETTE_CONFIG.emerald
  const lineWidth = Math.max(2, Math.round(canvasWidth / 600))
  const fontSize = Math.max(12, Math.round(canvasWidth / 80))

  ctx.save()

  ctx.lineWidth = lineWidth
  ctx.strokeStyle = colors.stroke
  ctx.fillStyle = colors.fill
  ctx.setLineDash(isDashed ? [lineWidth * 2, lineWidth * 2] : [])

  ctx.fillRect(rect.x, rect.y, rect.width, rect.height)
  ctx.strokeRect(rect.x, rect.y, rect.width, rect.height)

  if (label && label.trim() !== '') {
    ctx.setLineDash([])
    ctx.font = `600 ${fontSize}px sans-serif`

    const textWidth = ctx.measureText(label).width
    const paddingX = Math.round(fontSize * 0.4)
    const paddingY = Math.round(fontSize * 0.25)
    const badgeWidth = textWidth + paddingX * 2
    const badgeHeight = fontSize + paddingY * 2
    const radius = Math.max(3, Math.round(fontSize * 0.25))

    const badgeY =
      rect.y - badgeHeight - 2 >= 0
        ? rect.y - badgeHeight - 2
        : rect.y + lineWidth
    const badgeX = Math.max(0, Math.min(rect.x, canvasWidth - badgeWidth))

    ctx.fillStyle = colors.badgeBg
    ctx.beginPath()
    if (typeof ctx.roundRect === 'function') {
      ctx.roundRect(badgeX, badgeY, badgeWidth, badgeHeight, radius)
    } else {
      ctx.rect(badgeX, badgeY, badgeWidth, badgeHeight)
    }
    ctx.fill()

    ctx.fillStyle = colors.badgeText
    ctx.textBaseline = 'middle'
    ctx.fillText(label, badgeX + paddingX, badgeY + badgeHeight / 2)
  }

  ctx.restore()
}

function drawLandmarks(
  ctx: OverlayContext,
  landmarks: LandmarkPoint[],
  canvasWidth: number,
  canvasHeight: number
) {
  const pointRadius = Math.max(1.5, Math.round(canvasWidth / 800))
  ctx.save()
  ctx.fillStyle = '#fde047'
  ctx.strokeStyle = 'rgba(0, 0, 0, 0.8)'
  ctx.lineWidth = 1

  const isPixelSpace = landmarks.some(
    ([x, y]) => Number.isFinite(x) && Number.isFinite(y) && (x > 2 || y > 2)
  )

  for (const [rawX, rawY] of landmarks) {
    if (!Number.isFinite(rawX) || !Number.isFinite(rawY)) continue
    if (rawX < 0 || rawY < 0) continue

    if (!isPixelSpace && (rawX > 1 || rawY > 1)) continue

    const px = isPixelSpace ? rawX : rawX * canvasWidth
    const py = isPixelSpace ? rawY : rawY * canvasHeight
    if (px > canvasWidth || py > canvasHeight) continue

    ctx.beginPath()
    ctx.arc(px, py, pointRadius, 0, Math.PI * 2)
    ctx.fill()
    ctx.stroke()
  }

  ctx.restore()
}

/**
 * 在已绘制底图的画布上叠加检测框、标签徽章与关键点。
 *
 * 调用方负责先 `drawImage` 铺底；本函数只画 overlay，便于主线程与 Worker
 * 复用同一坐标系（`width`/`height` 必须是底图的自然尺寸）。
 */
export function paintOverlay(
  ctx: OverlayContext,
  width: number,
  height: number,
  options: OverlayPaintOptions
): void {
  const {
    primaryBox,
    primaryLabel,
    secondaryBox,
    secondaryLabel,
    palette = 'emerald',
    secondaryPalette = 'violet',
    landmarks,
  } = options

  if (isValidBox(secondaryBox)) {
    const secRect = toAbsoluteCoords(secondaryBox, width, height)
    drawBoxAndLabel(ctx, secRect, secondaryLabel, secondaryPalette, width, true)
  }

  if (isValidBox(primaryBox)) {
    const primRect = toAbsoluteCoords(primaryBox, width, height)
    drawBoxAndLabel(ctx, primRect, primaryLabel, palette, width, false)
  }

  if (landmarks && landmarks.length > 0) {
    drawLandmarks(ctx, landmarks, width, height)
  }
}
