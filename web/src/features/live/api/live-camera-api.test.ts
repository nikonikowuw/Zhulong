import type { AxiosResponse } from 'axios'
import { describe, expect, it, vi } from 'vitest'
import { apiClient, type ApiResponse } from '@/lib/api-client'
import {
  fetchLiveCameras,
  type BackendCameraListResponse,
  type BackendStreamResponse,
} from './live-camera-api'

function mockResponse<T>(data: T): AxiosResponse<ApiResponse<T>> {
  return {
    data: { code: 'OK', message: 'success', data },
    status: 200,
    statusText: 'OK',
    headers: {},
    config: {} as AxiosResponse['config'],
  }
}

function stream(
  overrides: Partial<BackendStreamResponse> &
    Pick<BackendStreamResponse, 'role'>
): BackendStreamResponse {
  return {
    id: 1,
    protocol: 'rtsp',
    codec: 'H264',
    width: 1920,
    height: 1080,
    ...overrides,
  }
}

function baseCamera(
  overrides: Partial<BackendCameraListResponse['items'][number]> = {}
): BackendCameraListResponse['items'][number] {
  return {
    id: 'cam-01',
    name: '大门',
    enabled: true,
    health: 'online',
    session: 'running',
    streams: [],
    ...overrides,
  }
}

function mockCameras(
  items: BackendCameraListResponse['items']
): BackendCameraListResponse {
  const payload = { items, total: items.length }
  vi.spyOn(apiClient, 'get').mockResolvedValueOnce(mockResponse(payload))
  return payload
}

describe('fetchLiveCameras', () => {
  it('adapts a full main stream', async () => {
    mockCameras([
      baseCamera({
        streams: [
          stream({
            role: 'main',
            host: '192.168.1.10',
            codec: 'H265',
            width: 2560,
            height: 1440,
          }),
        ],
      }),
    ])

    const [cam] = await fetchLiveCameras()
    expect(cam.host).toBe('192.168.1.10')
    expect(cam.resolution).toBe('2560x1440')
    expect(cam.codec).toBe('h265')
    expect(cam.status).toBe('online')
    expect(cam.hasSubStream).toBe(false)
  })

  it('uses the backend-provided host verbatim without deriving it from rtspUrl', async () => {
    mockCameras([
      baseCamera({
        streams: [
          stream({
            role: 'main',
            // 有凭据的完整 URL 存在，但后端没有下发 host —— 前端不得自行解析
            rtspUrl: 'rtsp://admin:pass@192.168.1.10:554/live/main',
            host: '',
          }),
        ],
      }),
    ])

    const [cam] = await fetchLiveCameras()
    expect(cam.host).toBeUndefined()
  })

  it('normalizes casing and blank hosts', async () => {
    mockCameras([
      baseCamera({
        id: 'cam-upper',
        streams: [stream({ role: 'main', host: 'CAM.Local' })],
      }),
      baseCamera({
        id: 'cam-blank',
        streams: [stream({ role: 'main', host: '   ' })],
      }),
    ])

    const [upper, blank] = await fetchLiveCameras()
    expect(upper.host).toBe('cam.local')
    expect(blank.host).toBeUndefined()
  })

  it('falls back to the sub stream host when main stream is absent', async () => {
    mockCameras([
      baseCamera({
        streams: [
          stream({
            role: 'sub',
            host: '172.16.0.9',
            width: 704,
            height: 576,
          }),
        ],
      }),
    ])

    const [cam] = await fetchLiveCameras()
    expect(cam.host).toBe('172.16.0.9')
    expect(cam.hasSubStream).toBe(true)
  })

  it('leaves host and resolution empty when the backend provides no usable data', async () => {
    mockCameras([
      // 解密失败：后端不提供 host，宽高为 0
      baseCamera({
        id: 'cam-enc',
        streams: [stream({ role: 'main', host: '', width: 0, height: 0 })],
      }),
      // 完全没有码流
      baseCamera({ id: 'cam-empty', health: 'unknown', session: 'idle' }),
    ])

    const cameras = await fetchLiveCameras()
    expect(cameras).toHaveLength(2)
    for (const cam of cameras) {
      expect(cam.host).toBeUndefined()
      expect(cam.resolution).toBeUndefined()
    }
  })

  it('rejects malformed backend camera data at runtime', async () => {
    const invalidData: unknown = {
      items: [
        {
          ...baseCamera(),
          streams: [
            {
              ...stream({ role: 'main' }),
              width: '1920',
            },
          ],
        },
      ],
      total: 1,
    }
    vi.spyOn(apiClient, 'get').mockResolvedValueOnce(mockResponse(invalidData))

    await expect(fetchLiveCameras()).rejects.toThrow()
  })
})
