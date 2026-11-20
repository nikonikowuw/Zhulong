import { fetchImageAsBlob } from './media-fetch'
import { paintOverlay, type OverlayPaintOptions } from './overlay-paint'

export { isValidBox, toAbsoluteCoords, type RectCoords } from './media-fetch'

export { type PaletteKey } from './overlay-paint'

/** 主线程烧录选项 = 纯绘制描述 + 拉流/编码参数 */
export interface OverlayBurnOptions extends OverlayPaintOptions {
  /** 取消信号 */
  signal?: AbortSignal
  /** 输出 MIME 类型，默认 'image/jpeg' */
  mimeType?: string
  /** 输出质量 (0~1)，默认 0.85 */
  quality?: number
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

/**
 * 离屏 Canvas 检测框与研判标签烧录引擎（主线程实现）。
 *
 * 正常导出走 `export-image.worker.ts` 里的 Worker 版本；这里是环境不支持
 * Worker/OffscreenCanvas 时的回退路径，两条路径共用 `paintOverlay` 的绘制核，
 * 视觉结果一致。
 */
export async function burnOverlayOnImage(
  imageSource: string | Blob,
  options: OverlayBurnOptions = {}
): Promise<Blob> {
  const {
    signal,
    mimeType = 'image/jpeg',
    quality = 0.85,
    ...paintOptions
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
    paintOverlay(ctx, naturalWidth, naturalHeight, paintOptions)
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
