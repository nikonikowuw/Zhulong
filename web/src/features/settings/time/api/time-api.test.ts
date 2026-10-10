import type { AxiosResponse } from 'axios'
import { describe, expect, it, vi } from 'vitest'
import { apiClient, type ApiResponse } from '@/lib/api-client'
import { type SystemTimeStatus } from '../types'
import { timeApi } from './time-api'

function mockResponse<T>(data: T): AxiosResponse<ApiResponse<T>> {
  return {
    data: { code: 'OK', message: 'success', data },
    status: 200,
    statusText: 'OK',
    headers: {},
    config: {} as AxiosResponse['config'],
  }
}

describe('timeApi', () => {
  const mockStatus: SystemTimeStatus = {
    currentTime: '2026-10-07T12:00:00Z',
    timezone: 'Asia/Shanghai',
    mode: 'ntp',
    ntpServers: ['ntp.aliyun.com', 'cn.pool.ntp.org'],
    syncIntervalSeconds: 900,
    syncStatus: {
      state: 'synchronized',
      lastSyncTime: '2026-10-07T11:45:00Z',
      lastSyncServer: 'ntp.aliyun.com',
      offsetMs: 12.3,
      rttMs: 25.1,
      errorMessage: '',
    },
    rtcStatus: 'normal',
    hasPermission: true,
  }

  it('getStatus requests GET /system/time', async () => {
    vi.spyOn(apiClient, 'get').mockResolvedValueOnce(mockResponse(mockStatus))

    const res = await timeApi.getStatus()
    expect(res).toEqual(mockStatus)
    expect(apiClient.get).toHaveBeenCalledWith('/system/time')
  })

  it('updateConfig sends PUT /system/time/config', async () => {
    vi.spyOn(apiClient, 'put').mockResolvedValueOnce(mockResponse(mockStatus))

    const payload = {
      mode: 'ntp' as const,
      ntpServers: ['ntp.aliyun.com'],
      syncIntervalSeconds: 600,
      timezone: 'UTC',
    }
    const res = await timeApi.updateConfig(payload)
    expect(res).toEqual(mockStatus)
    expect(apiClient.put).toHaveBeenCalledWith('/system/time/config', payload)
  })

  it('syncNow sends POST /system/time/sync', async () => {
    vi.spyOn(apiClient, 'post').mockResolvedValueOnce(mockResponse(mockStatus))

    const res = await timeApi.syncNow()
    expect(res).toEqual(mockStatus)
    expect(apiClient.post).toHaveBeenCalledWith('/system/time/sync')
  })

  it('setManualTime sends POST /system/time/manual', async () => {
    vi.spyOn(apiClient, 'post').mockResolvedValueOnce(mockResponse(mockStatus))

    const payload = { targetTime: '2026-10-07T13:00:00Z' }
    const res = await timeApi.setManualTime(payload)
    expect(res).toEqual(mockStatus)
    expect(apiClient.post).toHaveBeenCalledWith('/system/time/manual', payload)
  })
})
