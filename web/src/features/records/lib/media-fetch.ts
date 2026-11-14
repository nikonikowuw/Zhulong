import { getMediaUrl } from '@/lib/media-url'
import { type BoundingBox } from '../data/schema'

export interface RectCoords {
  x: number
  y: number
  width: number
  height: number
}

/** 校验 Box 坐标是否有效 */
export function isValidBox(box?: BoundingBox): box is BoundingBox {
  if (!box || !Array.isArray(box) || box.length !== 4) return false
  const [x1, y1, x2, y2] = box
  return x2 > x1 && y2 > y1
}

/** 将归一化坐标或像素坐标换算为绝对物理像素坐标，并做严格视口 clamp */
export function toAbsoluteCoords(
  box: BoundingBox,
  naturalWidth: number,
  naturalHeight: number
): RectCoords {
  const [x1, y1, x2, y2] = box
  const isNormalized = x2 <= 1.05 && y2 <= 1.05

  const absX1 = isNormalized ? x1 * naturalWidth : x1
  const absY1 = isNormalized ? y1 * naturalHeight : y1
  const absX2 = isNormalized ? x2 * naturalWidth : x2
  const absY2 = isNormalized ? y2 * naturalHeight : y2

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
 * 媒体拉取失败，保留 HTTP 状态码。
 *
 * 批量导出需要区分「重试有意义」（5xx / 408 / 429）与「重试纯属浪费」（404）。
 */
export class MediaFetchError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'MediaFetchError'
    this.status = status
  }
}

/** 带有凭据与取消信号的安全图片拉取（转 Blob） */
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
      throw new MediaFetchError(
        res.status,
        `Failed to load blob image: HTTP ${res.status}`
      )
    }
    return res.blob()
  }

  const url = getMediaUrl(imagePathOrUrl)
  const res = await fetch(url, {
    credentials: 'same-origin',
    signal,
  })

  if (!res.ok) {
    throw new MediaFetchError(
      res.status,
      `Failed to fetch media image: HTTP ${res.status}`
    )
  }

  return res.blob()
}
