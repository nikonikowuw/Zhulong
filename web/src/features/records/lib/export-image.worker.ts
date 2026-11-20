import { getMediaUrl } from '@/lib/media-url'
import {
  type ImageWorkerRequest,
  type ImageWorkerResponse,
} from './export-image-worker-protocol'
import { paintOverlay } from './overlay-paint'

/**
 * 导出图片 Worker：拉图 → 解码 → 烧录检测框 → 重新编码。
 *
 * 整条路径都在 Worker 线程里跑，主线程只负责收发消息，所以几百张图的
 * canvas 绘制与 JPEG 编码不会让页面掉帧。标签文案由主线程提前渲染成
 * 字符串随任务下发，Worker 不碰 i18n。
 */

interface WorkerScope {
  location: { origin: string }
  onmessage: ((event: MessageEvent<ImageWorkerRequest>) => void) | null
  postMessage(message: ImageWorkerResponse, transfer?: Transferable[]): void
}

const scope = self as unknown as WorkerScope

class ImageFetchError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ImageFetchError'
    this.status = status
  }
}

class UnsupportedEnvironmentError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'UnsupportedEnvironmentError'
  }
}

function resolveUrl(path: string): string {
  if (path.startsWith('blob:') || path.startsWith('data:')) {
    return path
  }
  return new URL(getMediaUrl(path), scope.location.origin).href
}

async function renderImage(request: ImageWorkerRequest): Promise<ArrayBuffer> {
  const response = await fetch(resolveUrl(request.imagePath), {
    credentials: 'same-origin',
  })

  if (!response.ok) {
    throw new ImageFetchError(
      response.status,
      `Failed to fetch media image: HTTP ${response.status}`
    )
  }

  const source = await response.blob()
  if (!request.overlay) {
    return source.arrayBuffer()
  }

  if (typeof OffscreenCanvas === 'undefined') {
    throw new UnsupportedEnvironmentError(
      'OffscreenCanvas is not available in this worker'
    )
  }

  const bitmap = await createImageBitmap(source)
  try {
    const canvas = new OffscreenCanvas(bitmap.width, bitmap.height)
    const ctx = canvas.getContext('2d')
    if (!ctx) {
      throw new Error('Failed to get 2D context from OffscreenCanvas')
    }

    ctx.drawImage(bitmap, 0, 0)
    paintOverlay(ctx, bitmap.width, bitmap.height, request.overlay)

    const encoded = await canvas.convertToBlob({
      type: request.mimeType,
      quality: request.quality,
    })
    return await encoded.arrayBuffer()
  } finally {
    bitmap.close()
  }
}

function describe(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

scope.onmessage = (event: MessageEvent<ImageWorkerRequest>) => {
  const request = event.data
  if (!request || typeof request.id !== 'number') return

  void renderImage(request).then(
    (bytes) => {
      scope.postMessage({ id: request.id, ok: true, bytes }, [bytes])
    },
    (err: unknown) => {
      scope.postMessage({
        id: request.id,
        ok: false,
        message: describe(err),
        status: err instanceof ImageFetchError ? err.status : undefined,
        unsupported: err instanceof UnsupportedEnvironmentError || undefined,
      })
    }
  )
}
