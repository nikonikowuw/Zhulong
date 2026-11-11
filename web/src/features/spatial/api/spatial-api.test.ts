import type { AxiosResponse, InternalAxiosRequestConfig } from 'axios'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient, type ApiResponse } from '@/lib/api-client'
import { spatialApi } from './spatial-api'

const defaultAdapter = apiClient.defaults.adapter

afterEach(() => {
  apiClient.defaults.adapter = defaultAdapter
})

function mockResponse<T>(data: T, status = 200): AxiosResponse<ApiResponse<T>> {
  return {
    data: { code: 'OK', message: 'success', data },
    status,
    statusText: 'OK',
    headers: {},
    config: {} as AxiosResponse['config'],
  }
}

describe('spatialApi', () => {
  it('getMaps fetches canvas maps', async () => {
    const mockMaps = [
      {
        id: 'map-1',
        name: 'Main Campus',
        imageUrl: '/api/v1/spatial/maps/map-1/image',
        width: 1920,
        height: 1080,
        createdAt: '2026-10-29T10:00:00Z',
        updatedAt: '2026-10-29T10:00:00Z',
      },
    ]
    vi.spyOn(apiClient, 'get').mockResolvedValueOnce(mockResponse(mockMaps))

    const maps = await spatialApi.getMaps()
    expect(maps).toEqual(mockMaps)
    expect(apiClient.get).toHaveBeenCalledWith('/spatial/maps')
  })

  it('updateMapName patches the name and parses the refreshed map', async () => {
    const renamed = {
      id: 'map-1',
      name: 'Renamed Campus',
      imageUrl: '/api/v1/spatial/maps/map-1/image',
      width: 1920,
      height: 1080,
      createdAt: '2026-10-29T10:00:00Z',
      updatedAt: '2026-10-29T11:00:00Z',
    }
    vi.spyOn(apiClient, 'patch').mockResolvedValueOnce(mockResponse(renamed))

    const result = await spatialApi.updateMapName('map-1', 'Renamed Campus')
    expect(result).toEqual(renamed)
    expect(apiClient.patch).toHaveBeenCalledWith('/spatial/maps/map-1', {
      name: 'Renamed Campus',
    })
  })

  it('getPositions fetches camera positions for a map', async () => {
    const mockPositions = [
      {
        cameraId: 'cam-1',
        cameraName: 'Gate East',
        x: 0.25,
        y: 0.35,
        angle: 90,
      },
    ]
    vi.spyOn(apiClient, 'get').mockResolvedValueOnce(
      mockResponse(mockPositions)
    )

    const positions = await spatialApi.getPositions('map-1')
    expect(positions).toEqual(mockPositions)
    expect(apiClient.get).toHaveBeenCalledWith('/spatial/maps/map-1/positions')
  })

  it('savePositions sends PUT payload to save positions', async () => {
    vi.spyOn(apiClient, 'put').mockResolvedValueOnce(
      mockResponse({ mapId: 'map-1', count: 1 })
    )

    const saved = await spatialApi.savePositions('map-1', [
      { cameraId: 'cam-1', x: 0.1, y: 0.2, angle: 45 },
    ])
    expect(saved).toEqual({ mapId: 'map-1', count: 1 })
    expect(apiClient.put).toHaveBeenCalledWith(
      '/spatial/maps/map-1/positions',
      {
        positions: [{ cameraId: 'cam-1', x: 0.1, y: 0.2, angle: 45 }],
      }
    )
  })

  it('queryTrajectory builds query string and parses response', async () => {
    const mockTraj = {
      mapId: 'map-1',
      targetType: 'vehicle' as const,
      targetKey: '粤B12345',
      nodes: [
        {
          seq: 1,
          cameraId: 'cam-1',
          cameraName: 'East Gate',
          x: 0.1,
          y: 0.2,
          firstSeenAt: '2026-10-29T10:00:00Z',
          lastSeenAt: '2026-10-29T10:00:15Z',
          dwellSec: 15,
          snapshotPath: 'crops/c1.jpg',
          score: 0.95,
          details: '粤B12345',
        },
      ],
    }
    vi.spyOn(apiClient, 'get').mockResolvedValueOnce(mockResponse(mockTraj))

    const res = await spatialApi.queryTrajectory({
      mapId: 'map-1',
      targetType: 'vehicle',
      plate: '粤B12345',
      startTime: '2026-10-29T09:00:00Z',
      endTime: '2026-10-29T11:00:00Z',
    })
    expect(res).toEqual(mockTraj)
  })

  it('uploadMap keeps FormData (and the file bytes) through real serialization', async () => {
    // 只替换 transport，让 Axios 的默认头合并与 `transformRequest` 真跑一遍：
    // 若请求头仍是默认的 application/json，FormData 会被序列化成
    // `{"file":{},"name":"…"}`，文件字节整段丢失（规范 §2.8）。
    const uploaded = {
      id: 'map-2',
      name: 'New Campus',
      imageUrl: '/api/v1/spatial/maps/map-2/image',
      width: 800,
      height: 600,
      createdAt: '2026-10-29T10:00:00Z',
      updatedAt: '2026-10-29T10:00:00Z',
    }
    const adapter = vi.fn(
      async (config: InternalAxiosRequestConfig<unknown>) => ({
        data: { code: 'OK', message: 'success', data: uploaded },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      })
    )
    apiClient.defaults.adapter = adapter

    const bytes = new Uint8Array([0x89, 0x50, 0x4e, 0x47])
    const file = new File([bytes], 'campus.png', { type: 'image/png' })
    await expect(spatialApi.uploadMap('New Campus', file)).resolves.toEqual(
      uploaded
    )

    const [config] = adapter.mock.calls[0]
    expect(config.url).toBe('/spatial/maps')
    expect(String(config.headers.getContentType())).toBe('multipart/form-data')

    const formData = config.data
    if (!(formData instanceof FormData)) {
      throw new Error('uploadMap must send FormData, not a JSON string')
    }
    expect(formData.get('name')).toBe('New Campus')
    const sent = formData.get('file')
    if (!(sent instanceof File)) {
      throw new Error('the file part must remain a File after serialization')
    }
    expect(sent.name).toBe('campus.png')
    expect(new Uint8Array(await sent.arrayBuffer())).toEqual(bytes)
  })
})
