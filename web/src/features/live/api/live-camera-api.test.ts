import { describe, expect, it } from 'vitest'
import { type Camera, type Stream } from '@/features/cameras/contract'
import { toLiveCameraItem } from './live-camera-api'

function stream(overrides: Partial<Stream> & Pick<Stream, 'role'>): Stream {
  return {
    id: 1,
    protocol: 'rtsp',
    rtspUrl: 'rtsp://192.168.1.10:554/live',
    host: '',
    transport: 'tcp',
    codec: 'H264',
    width: 1920,
    height: 1080,
    fpsString: '25',
    ...overrides,
  }
}

function camera(overrides: Partial<Camera> = {}): Camera {
  return {
    id: 'cam-01',
    name: '大门',
    enabled: true,
    revision: 1,
    health: 'online',
    session: 'running',
    degraded: false,
    stale: false,
    reason: '',
    streams: [],
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

describe('toLiveCameraItem', () => {
  it('adapts a full main stream', () => {
    const item = toLiveCameraItem(
      camera({
        streams: [
          stream({
            role: 'main',
            host: '192.168.1.10',
            codec: 'H265',
            width: 2560,
            height: 1440,
          }),
        ],
      })
    )

    expect(item).toEqual({
      id: 'cam-01',
      name: '大门',
      host: '192.168.1.10',
      status: 'online',
      hasSubStream: false,
      codec: 'h265',
      resolution: '2560x1440',
    })
  })

  it('never derives the host from rtspUrl when the backend omits it', () => {
    const item = toLiveCameraItem(
      camera({
        streams: [
          stream({
            role: 'main',
            rtspUrl: 'rtsp://admin:pass@192.168.1.10:554/live/main',
            host: '',
          }),
        ],
      })
    )

    expect(item.host).toBeUndefined()
  })

  it('normalizes host casing and blank values', () => {
    expect(
      toLiveCameraItem(
        camera({ streams: [stream({ role: 'main', host: 'CAM.Local' })] })
      ).host
    ).toBe('cam.local')

    expect(
      toLiveCameraItem(
        camera({ streams: [stream({ role: 'main', host: '   ' })] })
      ).host
    ).toBeUndefined()
  })

  it('falls back to the sub stream host when the main stream is absent', () => {
    const item = toLiveCameraItem(
      camera({
        streams: [
          stream({ role: 'sub', host: '172.16.0.9', width: 704, height: 576 }),
        ],
      })
    )

    expect(item.host).toBe('172.16.0.9')
    expect(item.hasSubStream).toBe(true)
  })

  it('leaves host, resolution and codec defaults empty when there is no stream', () => {
    const item = toLiveCameraItem(camera())

    expect(item.host).toBeUndefined()
    expect(item.resolution).toBeUndefined()
    expect(item.hasSubStream).toBe(false)
    expect(item.codec).toBe('h264')
  })

  it('treats zero dimensions as unknown instead of rendering 0x0', () => {
    const item = toLiveCameraItem(
      camera({ streams: [stream({ role: 'main', width: 0, height: 0 })] })
    )

    expect(item.resolution).toBeUndefined()
  })

  it.each([
    ['online', 'running', 'online'],
    ['offline', 'idle', 'offline'],
    ['error', 'idle', 'error'],
    ['unknown', 'reconnecting', 'reconnecting'],
    ['unknown', 'idle', 'offline'],
  ] as const)(
    'maps health=%s session=%s to status=%s',
    (health, session, expected) => {
      const item = toLiveCameraItem(camera({ health, session }))
      expect(item.status).toBe(expected)
    }
  )
})
