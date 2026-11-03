import { getMediaUrl } from '@/lib/media-url'
import { type BoundingBox } from '../data/schema'

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

export interface RectCoords {
  x: number
  y: number
  width: number
  height: number
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

/**
 * 校验 Box 坐标是否有效
 */
export function isValidBox(box?: BoundingBox): box is BoundingBox {
  if (!box || !Array.isArray(box) || box.length !== 4) return false
  const [x1, y1, x2, y2] = box
  return x2 > x1 && y2 > y1
}

/**
 * 将归一化坐标或像素坐标换算为绝对物理像素坐标，并做严格视口 clamp
 */
export function toAbsoluteCoords(
  box: BoundingBox,
  naturalWidth: number,
  naturalHeight: number
): RectCoords {
  const [x1, y1, x2, y2] = box
  const isNormalized = x2 <= 1.05 && y2 <= 1.05

  let absX1: number
  let absY1: number
  let absX2: number
  let absY2: number

  if (isNormalized) {
    absX1 = x1 * naturalWidth
    absY1 = y1 * naturalHeight
    absX2 = x2 * naturalWidth
    absY2 = y2 * naturalHeight
  } else {
    absX1 = x1
    absY1 = y1
    absX2 = x2
    absY2 = y2
  }

  // 视口安全限制
  const clampedX1 = Math.max(0, Math.min(absX1, naturalWidth))
  const clampedY1 = Math.max(0, Math.min(absY1, naturalHeight))
  const clampedX2 = Math.max(clampedX1, Math.min(absX2, naturalWidth))
  const clampedY2 = Math.max(clampedY1, Math.min(absY2, naturalHeight))

  return {
    x: Math.round(clampedX1),
    y: Math.round(clampedY1),
    width: Math.round(clampedX2 - clampedX1),
    height: Math.round(clampedY2 - clampedY1),
  }
}

function dataUriToBlob(dataUri: string): Blob {
  const parts = dataUri.split(',')
  const mime = parts[0].match(/:(.*?);/)?.[1] || 'image/png'
  const binary = atob(parts[1] || '')
  const array = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) {
    array[i] = binary.charCodeAt(i)
  }
  return new Blob([array], { type: mime })
}

/**
 * 带有凭据与取消信号的安全图片拉取（转 Blob）
 */
export async function fetchImageAsBlob(
  imagePathOrUrl: string,
  signal?: AbortSignal
): Promise<Blob> {
  if (imagePathOrUrl.startsWith('data:')) {
    return dataUriToBlob(imagePathOrUrl)
  }

  if (imagePathOrUrl.startsWith('blob:')) {
    const res = await fetch(imagePathOrUrl, { signal })
    if (!res.ok) {
      throw new Error(`Failed to load blob image: HTTP ${res.status}`)
    }
    return res.blob()
  }

  const url = getMediaUrl(imagePathOrUrl)
  const res = await fetch(url, {
    credentials: 'same-origin',
    signal,
  })

  if (!res.ok) {
    throw new Error(`Failed to fetch media image: HTTP ${res.status}`)
  }

  return res.blob()
}

/**
 * 绘制单层检测框与标签胶囊
 */
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

  // 1. 绘制半透明填充矩形与边框
  ctx.lineWidth = lineWidth
  ctx.strokeStyle = colors.stroke
  ctx.fillStyle = colors.fill
  if (isDashed) {
    ctx.setLineDash([lineWidth * 2, lineWidth * 2])
  } else {
    ctx.setLineDash([])
  }

  ctx.fillRect(rect.x, rect.y, rect.width, rect.height)
  ctx.strokeRect(rect.x, rect.y, rect.width, rect.height)

  // 2. 绘制研判标签胶囊
  if (label && label.trim() !== '') {
    ctx.setLineDash([])
    ctx.font = `600 ${fontSize}px sans-serif`

    const textMetrics = ctx.measureText(label)
    const textWidth = textMetrics.width
    const paddingX = Math.round(fontSize * 0.4)
    const paddingY = Math.round(fontSize * 0.25)
    const badgeWidth = textWidth + paddingX * 2
    const badgeHeight = fontSize + paddingY * 2
    const radius = Math.max(3, Math.round(fontSize * 0.25))

    // 顶边碰撞翻转 (Top Collision Flip)：
    // 若上方放不下，则向下翻转至框内上沿
    let badgeY: number
    if (rect.y - badgeHeight - 2 >= 0) {
      badgeY = rect.y - badgeHeight - 2
    } else {
      badgeY = rect.y + lineWidth
    }

    // 水平越界保护
    const badgeX = Math.max(0, Math.min(rect.x, canvasWidth - badgeWidth))

    // 绘制圆角胶囊底色
    ctx.fillStyle = colors.badgeBg
    ctx.beginPath()
    if (typeof ctx.roundRect === 'function') {
      ctx.roundRect(badgeX, badgeY, badgeWidth, badgeHeight, radius)
    } else {
      ctx.rect(badgeX, badgeY, badgeWidth, badgeHeight)
    }
    ctx.fill()

    // 绘制标签文字
    ctx.fillStyle = colors.badgeText
    ctx.textBaseline = 'middle'
    ctx.fillText(label, badgeX + paddingX, badgeY + badgeHeight / 2)
  }

  ctx.restore()
}

/**
 * 离屏 Canvas 检测框与研判标签烧录引擎
 */
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

  // 检查取消信号
  if (signal?.aborted) {
    throw new DOMException('Burn overlay operation was aborted', 'AbortError')
  }

  // 1. 获取图片 Blob
  const sourceBlob =
    typeof imageSource === 'string'
      ? await fetchImageAsBlob(imageSource, signal)
      : imageSource

  if (signal?.aborted) {
    throw new DOMException('Burn overlay operation was aborted', 'AbortError')
  }

  // 2. 解码图片位图或元素（带异常优雅降级）
  let naturalWidth = 0
  let naturalHeight = 0
  let renderSource: ImageBitmap | HTMLImageElement | null = null

  if (typeof createImageBitmap === 'function') {
    try {
      const bitmap = await createImageBitmap(sourceBlob)
      naturalWidth = bitmap.width
      naturalHeight = bitmap.height
      renderSource = bitmap
    } catch {
      renderSource = null
    }
  }

  if (!renderSource) {
    // 降级使用 HTMLImageElement
    const objectUrl = URL.createObjectURL(sourceBlob)
    const img = new Image()
    await new Promise<void>((resolve, reject) => {
      img.onload = () => resolve()
      img.onerror = () => reject(new Error('Failed to decode image'))
      img.src = objectUrl
    })
    URL.revokeObjectURL(objectUrl)
    naturalWidth = img.naturalWidth
    naturalHeight = img.naturalHeight
    renderSource = img
  }

  // 3. 准备 Canvas 尺寸
  let outputBlob: Blob
  const hasSecondary = isValidBox(secondaryBox)
  const hasPrimary = isValidBox(primaryBox)

  // 支持 OffscreenCanvas 则优先使用，降级到 DOM Canvas
  if (typeof OffscreenCanvas !== 'undefined') {
    const offCanvas = new OffscreenCanvas(naturalWidth, naturalHeight)
    const ctx = offCanvas.getContext('2d')
    if (!ctx) {
      if ('close' in renderSource) renderSource.close()
      throw new Error('Failed to get 2D context from OffscreenCanvas')
    }

    ctx.drawImage(renderSource, 0, 0, naturalWidth, naturalHeight)

    if (hasSecondary) {
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

    if (hasPrimary) {
      const primRect = toAbsoluteCoords(primaryBox, naturalWidth, naturalHeight)
      drawBoxAndLabel(ctx, primRect, primaryLabel, palette, naturalWidth, false)
    }

    outputBlob = await offCanvas.convertToBlob({ type: mimeType, quality })

    // 释放资源
    offCanvas.width = 0
    offCanvas.height = 0
  } else {
    const canvas = document.createElement('canvas')
    canvas.width = naturalWidth
    canvas.height = naturalHeight
    const ctx = canvas.getContext('2d')
    if (!ctx) {
      if ('close' in renderSource) renderSource.close()
      throw new Error('Failed to get 2D context from HTMLCanvasElement')
    }

    ctx.drawImage(renderSource, 0, 0, naturalWidth, naturalHeight)

    if (hasSecondary) {
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

    if (hasPrimary) {
      const primRect = toAbsoluteCoords(primaryBox, naturalWidth, naturalHeight)
      drawBoxAndLabel(ctx, primRect, primaryLabel, palette, naturalWidth, false)
    }

    outputBlob = await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob(
        (b) => {
          if (b) resolve(b)
          else reject(new Error('Canvas toBlob returned null'))
        },
        mimeType,
        quality
      )
    })

    // 释放显存
    canvas.width = 0
    canvas.height = 0
  }

  // 释放位图资源
  if ('close' in renderSource) {
    renderSource.close()
  }

  return outputBlob
}
