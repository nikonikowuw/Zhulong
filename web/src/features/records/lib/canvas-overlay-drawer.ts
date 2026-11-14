import { type BoundingBox } from '../data/schema'
import {
  fetchImageAsBlob,
  isValidBox,
  toAbsoluteCoords,
  type RectCoords,
} from './media-fetch'

export { isValidBox, toAbsoluteCoords, type RectCoords }

export type PaletteKey = 'emerald' | 'rose' | 'amber' | 'sky' | 'violet'

export interface OverlayBurnOptions {
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
  /** 取消信号 */
  signal?: AbortSignal
  /** 输出 MIME 类型，默认 'image/jpeg' */
  mimeType?: string
  /** 输出质量 (0~1)，默认 0.85 */
  quality?: number
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

function drawBoxAndLabel(
  ctx: CanvasRenderingContext2D | OffscreenCanvasRenderingContext2D,
  rect: RectCoords,
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

async function decodeImageSource(sourceBlob: Blob): Promise<{
  width: number
  height: number
  source: ImageBitmap | HTMLImageElement
}> {
  if (typeof createImageBitmap === 'function') {
    try {
      const bitmap = await createImageBitmap(sourceBlob)
      return { width: bitmap.width, height: bitmap.height, source: bitmap }
    } catch {
      // 降级到 HTMLImageElement
    }
  }

  const objectUrl = URL.createObjectURL(sourceBlob)
  const img = new Image()
  try {
    await new Promise<void>((resolve, reject) => {
      img.onload = () => resolve()
      img.onerror = () => reject(new Error('Failed to decode image'))
      img.src = objectUrl
    })
  } finally {
    URL.revokeObjectURL(objectUrl)
  }
  return { width: img.naturalWidth, height: img.naturalHeight, source: img }
}

/** 离屏 Canvas 检测框与研判标签烧录引擎 */
export async function burnOverlayOnImage(
  imageSource: string | Blob,
  options: OverlayBurnOptions = {}
): Promise<Blob> {
  const {
    primaryBox,
    primaryLabel,
    secondaryBox,
    secondaryLabel,
    palette = 'emerald',
    secondaryPalette = 'violet',
    signal,
    mimeType = 'image/jpeg',
    quality = 0.85,
  } = options

  if (signal?.aborted) {
    throw new DOMException('Burn overlay operation was aborted', 'AbortError')
  }

  const sourceBlob =
    typeof imageSource === 'string'
      ? await fetchImageAsBlob(imageSource, signal)
      : imageSource

  if (signal?.aborted) {
    throw new DOMException('Burn overlay operation was aborted', 'AbortError')
  }

  const {
    width: naturalWidth,
    height: naturalHeight,
    source: renderSource,
  } = await decodeImageSource(sourceBlob)

  const paint = (
    ctx: CanvasRenderingContext2D | OffscreenCanvasRenderingContext2D
  ) => {
    ctx.drawImage(renderSource, 0, 0, naturalWidth, naturalHeight)

    if (isValidBox(secondaryBox)) {
      const secRect = toAbsoluteCoords(
        secondaryBox,
        naturalWidth,
        naturalHeight
      )
      drawBoxAndLabel(
        ctx,
        secRect,
        secondaryLabel,
        secondaryPalette,
        naturalWidth,
        true
      )
    }

    if (isValidBox(primaryBox)) {
      const primRect = toAbsoluteCoords(primaryBox, naturalWidth, naturalHeight)
      drawBoxAndLabel(ctx, primRect, primaryLabel, palette, naturalWidth, false)
    }
  }

  try {
    if (typeof OffscreenCanvas !== 'undefined') {
      const offCanvas = new OffscreenCanvas(naturalWidth, naturalHeight)
      const ctx = offCanvas.getContext('2d')
      if (!ctx) {
        throw new Error('Failed to get 2D context from OffscreenCanvas')
      }
      paint(ctx)
      const blob = await offCanvas.convertToBlob({ type: mimeType, quality })
      offCanvas.width = 0
      offCanvas.height = 0
      return blob
    }

    const canvas = document.createElement('canvas')
    canvas.width = naturalWidth
    canvas.height = naturalHeight
    const ctx = canvas.getContext('2d')
    if (!ctx) {
      throw new Error('Failed to get 2D context from HTMLCanvasElement')
    }
    paint(ctx)
    const blob = await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob(
        (b) => {
          if (b) resolve(b)
          else reject(new Error('Canvas toBlob returned null'))
        },
        mimeType,
        quality
      )
    })
    canvas.width = 0
    canvas.height = 0
    return blob
  } finally {
    if ('close' in renderSource) {
      renderSource.close()
    }
  }
}
