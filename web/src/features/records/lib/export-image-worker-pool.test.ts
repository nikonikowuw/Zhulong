import { afterEach, describe, expect, it } from 'vitest'
import {
  getImageWorkerConcurrency,
  runImageJobInPool,
  terminateImageWorkerPool,
} from './export-image-worker-pool'
import { type ImageJob } from './export-types'

/**
 * 真实 Worker 测试：不 mock Worker/OffscreenCanvas，验证主线程只收发消息时
 * 图片确实在 Worker 里完成「解码 → 烧录 → 编码」，并回传可用的 JPEG 字节。
 */

const SOURCE_WIDTH = 200
const SOURCE_HEIGHT = 120
const BOX: [number, number, number, number] = [0.1, 0.1, 0.8, 0.8]

async function makeSourceDataUrl(): Promise<string> {
  const canvas = new OffscreenCanvas(SOURCE_WIDTH, SOURCE_HEIGHT)
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('test canvas unavailable')
  ctx.fillStyle = '#ffffff'
  ctx.fillRect(0, 0, SOURCE_WIDTH, SOURCE_HEIGHT)
  const blob = await canvas.convertToBlob({ type: 'image/png' })

  return await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(blob)
  })
}

function makeJob(imagePath: string, withOverlay: boolean): ImageJob {
  return {
    tab: 'recognitions',
    recordId: 'rec-001',
    capturedAt: '2026-01-01T00:00:00.000Z',
    kind: 'scene',
    imagePath,
    overlayOptions: withOverlay
      ? {
          primaryBox: BOX,
          primaryLabel: '张三 (98%)',
          palette: 'emerald',
          landmarks: [[0.5, 0.4]],
        }
      : {},
  }
}

async function decode(bytes: Uint8Array): Promise<{
  width: number
  height: number
  pixel: (x: number, y: number) => [number, number, number, number]
}> {
  const bitmap = await createImageBitmap(
    new Blob([new Uint8Array(bytes)], { type: 'image/jpeg' })
  )
  const canvas = new OffscreenCanvas(bitmap.width, bitmap.height)
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('test canvas unavailable')
  ctx.drawImage(bitmap, 0, 0)
  const width = bitmap.width
  const height = bitmap.height
  bitmap.close()

  return {
    width,
    height,
    pixel: (x, y) => {
      const data = ctx.getImageData(x, y, 1, 1).data
      return [data[0], data[1], data[2], data[3]]
    },
  }
}

describe('export-image-worker-pool', () => {
  afterEach(() => {
    terminateImageWorkerPool()
  })

  it('reports the pool concurrency used by the pipeline', () => {
    expect(getImageWorkerConcurrency()).toBeGreaterThanOrEqual(1)
    expect(getImageWorkerConcurrency()).toBeLessThanOrEqual(3)
  })

  it('burns the overlay inside a worker and returns an encoded JPEG', async () => {
    const source = await makeSourceDataUrl()

    const bytes = await runImageJobInPool(makeJob(source, true))
    expect(bytes.byteLength).toBeGreaterThan(0)
    // JPEG SOI
    expect([bytes[0], bytes[1]]).toEqual([0xff, 0xd8])

    const decoded = await decode(bytes)
    expect(decoded.width).toBe(SOURCE_WIDTH)
    expect(decoded.height).toBe(SOURCE_HEIGHT)

    // 框内填充是 15% 透明度的 emerald，白色底图应被染绿：G 明显高于 R
    const [r, g] = decoded.pixel(90, 54)
    expect(r).toBeLessThan(240)
    expect(g - r).toBeGreaterThan(10)
  })

  it('returns the untouched image when there is nothing to burn', async () => {
    const source = await makeSourceDataUrl()

    const bytes = await runImageJobInPool(makeJob(source, false))
    const decoded = await decode(bytes)
    const [r, g, b] = decoded.pixel(90, 54)

    expect(r).toBeGreaterThan(245)
    expect(g).toBeGreaterThan(245)
    expect(b).toBeGreaterThan(245)
  })

  it('rejects immediately when the signal is already aborted', async () => {
    const controller = new AbortController()
    controller.abort()

    await expect(
      runImageJobInPool(
        makeJob('data:image/png;base64,', true),
        controller.signal
      )
    ).rejects.toMatchObject({ name: 'AbortError' })
  })

  it('aborts an in-flight task and recovers for the next run', async () => {
    const source = await makeSourceDataUrl()
    const controller = new AbortController()

    const pending = runImageJobInPool(makeJob(source, true), controller.signal)
    controller.abort()

    await expect(pending).rejects.toMatchObject({ name: 'AbortError' })

    terminateImageWorkerPool()
    const bytes = await runImageJobInPool(makeJob(source, true))
    expect(bytes.byteLength).toBeGreaterThan(0)
  })
})
