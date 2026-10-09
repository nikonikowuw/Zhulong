import type { AxiosResponse } from 'axios'
import { describe, expect, it, vi } from 'vitest'
import { apiClient, type ApiResponse } from '@/lib/api-client'
import { networkApi } from './network-api'

function mockResponse<T>(data: T): AxiosResponse<ApiResponse<T>> {
  return {
    data: { code: 'OK', message: 'success', data },
    status: 200,
    statusText: 'OK',
    headers: {},
    config: {} as AxiosResponse['config'],
  }
}

describe('networkApi', () => {
  it('getInterfaces requests /system/network/interfaces', async () => {
    const mockData = [
      {
        name: 'eth0',
        mac: '52:54:00:12:34:56',
        linkUp: true,
        mode: 'dhcp' as const,
        ipAddresses: ['192.168.1.100/24'],
        gateway: '192.168.1.1',
        dns: ['8.8.8.8'],
        isDefaultGw: true,
        isCurrent: true,
      },
    ]

    vi.spyOn(apiClient, 'get').mockResolvedValueOnce(mockResponse(mockData))

    const res = await networkApi.getInterfaces()
    expect(res).toEqual(mockData)
    expect(apiClient.get).toHaveBeenCalledWith('/system/network/interfaces')
  })

  it('applyConfig sends payload to /system/network/interfaces/:name/apply', async () => {
    const mockResp = {
      transactionId: 'tx-123',
      timeoutSec: 60,
      targetUrl: 'http://192.168.1.120:8080/settings/network',
      confirmToken: 'tok-abc',
    }

    vi.spyOn(apiClient, 'post').mockResolvedValueOnce(mockResponse(mockResp))

    const payload = {
      mode: 'static' as const,
      ipAddress: '192.168.1.120',
      subnetMask: '255.255.255.0',
      setDefault: true,
    }

    const res = await networkApi.applyConfig('eth0', payload)
    expect(res).toEqual(mockResp)
    expect(apiClient.post).toHaveBeenCalledWith(
      '/system/network/interfaces/eth0/apply',
      payload
    )
  })

  it('confirm calls /system/network/confirm with token', async () => {
    vi.spyOn(apiClient, 'post').mockResolvedValueOnce(
      mockResponse({ status: 'confirmed' })
    )

    const res = await networkApi.confirm('tok-123')
    expect(res).toEqual({ status: 'confirmed' })
    expect(apiClient.post).toHaveBeenCalledWith(
      '/system/network/confirm',
      { token: 'tok-123' },
      { params: { token: 'tok-123' } }
    )
  })

  it('rollback calls /system/network/rollback with token', async () => {
    vi.spyOn(apiClient, 'post').mockResolvedValueOnce(
      mockResponse({ status: 'rolled_back' })
    )

    const res = await networkApi.rollback('tok-123')
    expect(res).toEqual({ status: 'rolled_back' })
    expect(apiClient.post).toHaveBeenCalledWith(
      '/system/network/rollback',
      { token: 'tok-123' },
      { params: { token: 'tok-123' } }
    )
  })

  it('getStatus calls /system/network/status', async () => {
    const mockStatus = {
      transactionId: 'tx-999',
      status: 'pending_confirm' as const,
      interfaceName: 'eth0',
      confirmToken: 'tok-999',
      targetUrl: 'http://192.168.1.50:8080',
      timeoutSec: 45,
      expiresAt: '2026-10-09T12:00:00Z',
    }

    vi.spyOn(apiClient, 'get').mockResolvedValueOnce(mockResponse(mockStatus))

    const res = await networkApi.getStatus()
    expect(res).toEqual(mockStatus)
    expect(apiClient.get).toHaveBeenCalledWith('/system/network/status')
  })

  it('ping calls /system/network/ping with target', async () => {
    const mockPing = { reachable: true, rttMs: 12.34 }

    vi.spyOn(apiClient, 'post').mockResolvedValueOnce(mockResponse(mockPing))

    const res = await networkApi.ping('8.8.8.8')
    expect(res).toEqual(mockPing)
    expect(apiClient.post).toHaveBeenCalledWith('/system/network/ping', {
      target: '8.8.8.8',
    })
  })
})
