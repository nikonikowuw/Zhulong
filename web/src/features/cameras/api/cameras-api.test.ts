import type { AxiosResponse } from 'axios'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiClient, type ApiResponse } from '@/lib/api-client'
import { camerasApi } from './cameras-api'

function mockResponse<T>(data: T): AxiosResponse<ApiResponse<T>> {
  return {
    data: { code: 'OK', message: 'success', data },
    status: 200,
    statusText: 'OK',
    headers: {},
    config: {} as AxiosResponse['config'],
  }
}

describe('camerasApi', () => {
  it('getCameras requests /cameras with pagination params', async () => {
    const mockData = {
      items: [
        {
          id: 'cam-01',
          name: 'Main Gate',
          enabled: true,
          revision: 1,
          health: 'online' as const,
          session: 'running' as const,
          degraded: false,
          stale: false,
          reason: '',
          streams: [],
          createdAt: '2026-10-09T00:00:00Z',
          updatedAt: '2026-10-09T00:00:00Z',
        },
      ],
      total: 1,
      page: 1,
      pageSize: 10,
    }

    vi.spyOn(apiClient, 'get').mockResolvedValueOnce(mockResponse(mockData))

    const res = await camerasApi.getCameras({
      page: 1,
      pageSize: 10,
      search: 'Gate',
    })
    expect(res).toEqual(mockData)
    expect(apiClient.get).toHaveBeenCalledWith('/cameras', {
      params: { page: 1, pageSize: 10, search: 'Gate' },
    })
  })

  it('createCamera posts payload to /cameras', async () => {
    const payload = {
      name: 'New Camera',
      enabled: true,
      mainStream: {
        role: 'main' as const,
        protocol: 'rtsp',
        rtspUrl: 'rtsp://admin:pass@192.168.1.50:554/live',
        transport: 'tcp' as const,
      },
    }

    const mockCreated = {
      id: 'cam-created',
      name: 'New Camera',
      enabled: true,
      revision: 1,
      health: 'unknown' as const,
      session: 'idle' as const,
      degraded: false,
      stale: false,
      reason: '',
      streams: [],
      createdAt: '2026-10-09T00:00:00Z',
      updatedAt: '2026-10-09T00:00:00Z',
    }

    vi.spyOn(apiClient, 'post').mockResolvedValueOnce(mockResponse(mockCreated))

    const res = await camerasApi.createCamera(payload)
    expect(res).toEqual(mockCreated)
    expect(apiClient.post).toHaveBeenCalledWith('/cameras', payload)
  })

  it('diagnose posts to /cameras/:id/diagnose', async () => {
    const mockDiagnose = {
      cameraId: 'cam-01',
      message: 'Probe successful',
      state: {
        cameraId: 'cam-01',
        enabled: true,
        revision: 1,
        health: 'online',
        session: 'running',
        degraded: false,
        stale: false,
      },
    }

    vi.spyOn(apiClient, 'post').mockResolvedValueOnce(
      mockResponse(mockDiagnose)
    )

    const res = await camerasApi.diagnose('cam-01')
    expect(res).toEqual(mockDiagnose)
    expect(apiClient.post).toHaveBeenCalledWith('/cameras/cam-01/diagnose')
  })

  it('getCredentials requests /cameras/:id/credentials', async () => {
    const mockCreds = {
      cameraId: 'cam-01',
      credentials: {
        main: 'rtsp://admin:plaintext@192.168.1.100:554/live',
      },
    }

    vi.spyOn(apiClient, 'get').mockResolvedValueOnce(mockResponse(mockCreds))

    const res = await camerasApi.getCredentials('cam-01')
    expect(res).toEqual(mockCreds)
    expect(apiClient.get).toHaveBeenCalledWith('/cameras/cam-01/credentials')
  })

  it('deleteCamera sends DELETE to /cameras/:id', async () => {
    const mockDelete = { id: 'cam-01', deleted: true }
    vi.spyOn(apiClient, 'delete').mockResolvedValueOnce(
      mockResponse(mockDelete)
    )

    const res = await camerasApi.deleteCamera('cam-01')
    expect(res).toEqual(mockDelete)
    expect(apiClient.delete).toHaveBeenCalledWith('/cameras/cam-01')
  })

  describe('getAll', () => {
    // 前面的用例已经 spy 过同一个方法，`mock.calls` 会串到用例之间
    beforeEach(() => {
      vi.restoreAllMocks()
    })

    function cameraItem(id: string) {
      return {
        id,
        name: `Camera ${id}`,
        enabled: true,
        revision: 1,
        health: 'online' as const,
        session: 'running' as const,
        degraded: false,
        stale: false,
        reason: '',
        streams: [],
        createdAt: '2026-01-01T00:00:00Z',
        updatedAt: '2026-01-01T00:00:00Z',
      }
    }

    it('stops after the first page when total fits inside the page limit', async () => {
      const getSpy = vi.spyOn(apiClient, 'get').mockResolvedValueOnce(
        mockResponse({
          items: [cameraItem('cam-01'), cameraItem('cam-02')],
          total: 2,
          page: 1,
          pageSize: 100,
        })
      )

      const res = await camerasApi.getAll()

      expect(res.items).toHaveLength(2)
      expect(getSpy).toHaveBeenCalledTimes(1)
      expect(getSpy).toHaveBeenCalledWith('/cameras', {
        params: { page: 1, pageSize: 100 },
      })
    })

    it('merges every page when total exceeds the single-page limit', async () => {
      // 后端单页上限 100，而 MaxCameraCapacity 为 128，因此必然出现第二页
      const firstPage = Array.from({ length: 100 }, (_, i) =>
        cameraItem(`cam-${String(i).padStart(3, '0')}`)
      )
      const secondPage = Array.from({ length: 28 }, (_, i) =>
        cameraItem(`cam-1${String(i).padStart(2, '0')}`)
      )

      const getSpy = vi
        .spyOn(apiClient, 'get')
        .mockResolvedValueOnce(
          mockResponse({
            items: firstPage,
            total: 128,
            page: 1,
            pageSize: 100,
          })
        )
        .mockResolvedValueOnce(
          mockResponse({
            items: secondPage,
            total: 128,
            page: 2,
            pageSize: 100,
          })
        )

      const res = await camerasApi.getAll()

      expect(res.items).toHaveLength(128)
      expect(res.total).toBe(128)
      expect(getSpy).toHaveBeenCalledTimes(2)
      expect(getSpy).toHaveBeenNthCalledWith(2, '/cameras', {
        params: { page: 2, pageSize: 100 },
      })
      // 不重复、不丢失
      expect(new Set(res.items.map((c) => c.id)).size).toBe(128)
    })
  })
})
