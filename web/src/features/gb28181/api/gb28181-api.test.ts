import type { AxiosResponse } from 'axios'
import { describe, expect, it, vi } from 'vitest'
import { apiClient, type ApiResponse } from '@/lib/api-client'
import { gb28181Api } from './gb28181-api'

function mockResponse<T>(data: T): AxiosResponse<ApiResponse<T>> {
  return {
    data: { code: 'OK', message: 'success', data },
    status: 200,
    statusText: 'OK',
    headers: {},
    config: {} as AxiosResponse['config'],
  }
}

const configFixture = {
  enabled: true,
  sipId: '34020000002000000001',
  sipRealm: '3402000000',
  sipListen: '0.0.0.0:5060',
  sipTransport: 'both',
  securityProfile: 'standard',
  mediaBindAddress: '0.0.0.0',
  mediaAdvertisedIp: '192.168.1.10',
  rtpPortMin: 30000,
  rtpPortMax: 30100,
  requireRegisteredDevice: true,
}

const deviceFixture = {
  deviceId: '34020000001320000001',
  name: '东门球机',
  manufacturer: null,
  model: null,
  firmware: null,
  transport: 'udp',
  remoteAddr: null,
  expiresSeconds: 3600,
  state: 'offline',
  lastKeepaliveAt: null,
  registeredAt: null,
  createdAt: '2026-10-25T08:00:00Z',
  updatedAt: '2026-10-25T08:00:00Z',
}

describe('gb28181Api', () => {
  it('getConfig requests /gb28181/config and parses the envelope', async () => {
    const payload = {
      config: configFixture,
      runtime: {
        startupConfig: configFixture,
        state: 'running',
        detail: '',
        advertisedIp: '192.168.1.10',
        restartRequired: false,
        mediaCapacity: 50,
      },
    }
    vi.spyOn(apiClient, 'get').mockResolvedValueOnce(mockResponse(payload))

    const res = await gb28181Api.getConfig()

    expect(res).toEqual(payload)
    expect(apiClient.get).toHaveBeenCalledWith('/gb28181/config')
  })

  it('updateConfig sends the complete target configuration', async () => {
    const payload = {
      config: configFixture,
      runtime: {
        startupConfig: configFixture,
        state: 'disabled',
        detail: '',
        advertisedIp: '',
        restartRequired: true,
        mediaCapacity: 50,
      },
    }
    vi.spyOn(apiClient, 'put').mockResolvedValueOnce(mockResponse(payload))

    const res = await gb28181Api.updateConfig(configFixture)

    expect(res.runtime.restartRequired).toBe(true)
    expect(apiClient.put).toHaveBeenCalledWith('/gb28181/config', configFixture)
  })

  it('getRuntime parses the counters snapshot', async () => {
    const snapshot = {
      state: 'running',
      restartRequired: false,
      devices: { total: 2, online: 1 },
      sessions: { active: 0 },
      portPool: { pairsTotal: 50, pairsUsed: 0, exhaustedHits: 0 },
      ingress: {
        oversizedMessages: 4,
        rejectedConns: 0,
        inboundLimited: 7,
        outboundLimited: 1,
        activeConns: 2,
      },
      generatedAt: '2026-10-25T08:00:00Z',
    }
    vi.spyOn(apiClient, 'get').mockResolvedValueOnce(mockResponse(snapshot))

    const res = await gb28181Api.getRuntime()

    expect(res.portPool.pairsTotal).toBe(50)
    expect(res.ingress.inboundLimited).toBe(7)
    expect(apiClient.get).toHaveBeenCalledWith('/gb28181/runtime')
  })

  it('listDevices unwraps items and tolerates null metadata', async () => {
    vi.spyOn(apiClient, 'get').mockResolvedValueOnce(
      mockResponse({ items: [deviceFixture] })
    )

    const devices = await gb28181Api.listDevices()

    expect(devices).toHaveLength(1)
    expect(devices[0].deviceId).toBe('34020000001320000001')
    expect(devices[0].manufacturer).toBeNull()
    expect(apiClient.get).toHaveBeenCalledWith('/gb28181/devices')
  })

  it('listDevices rejects a payload that is not the device list contract', async () => {
    vi.spyOn(apiClient, 'get').mockResolvedValueOnce(
      mockResponse({ items: [{ deviceId: 1 }] })
    )

    await expect(gb28181Api.listDevices()).rejects.toThrow()
  })

  it('upsertDevice escapes the device ID and sends only provided fields', async () => {
    vi.spyOn(apiClient, 'put').mockResolvedValueOnce(
      mockResponse(deviceFixture)
    )

    await gb28181Api.upsertDevice('34020000001320000001', {
      password: 's3cret',
      transport: 'udp',
    })

    expect(apiClient.put).toHaveBeenCalledWith(
      '/gb28181/devices/34020000001320000001',
      { password: 's3cret', transport: 'udp' }
    )
  })

  it('listChannels returns parsed channel items', async () => {
    const channelFixture = {
      deviceId: '34020000001320000001',
      channelId: '34020000001310000001',
      name: 'Front Gate Camera',
      manufacturer: 'Hikvision',
      model: 'DS-2CD',
      owner: null,
      civilCode: null,
      address: null,
      parental: 0,
      parentId: null,
      safetyWay: null,
      registerWay: null,
      ptzType: 1,
      resolutionWidth: 1920,
      resolutionHeight: 1080,
      cameraId: null,
      createdAt: '2026-10-25T08:00:00Z',
      updatedAt: '2026-10-25T08:00:00Z',
    }
    vi.spyOn(apiClient, 'get').mockResolvedValueOnce(
      mockResponse({ items: [channelFixture] })
    )

    const channels = await gb28181Api.listChannels('34020000001320000001')

    expect(channels).toHaveLength(1)
    expect(channels[0].channelId).toBe('34020000001310000001')
    expect(channels[0].resolutionWidth).toBe(1920)
    expect(apiClient.get).toHaveBeenCalledWith(
      '/gb28181/devices/34020000001320000001/channels'
    )
  })

  it('syncCatalog issues POST to catalog endpoint', async () => {
    vi.spyOn(apiClient, 'post').mockResolvedValueOnce(
      mockResponse({ deviceId: '34020000001320000001', total: 5 })
    )

    const res = await gb28181Api.syncCatalog('34020000001320000001')

    expect(res.total).toBe(5)
    expect(apiClient.post).toHaveBeenCalledWith(
      '/gb28181/devices/34020000001320000001/catalog',
      {}
    )
  })

  it('addChannelAsCamera posts to camera binding endpoint', async () => {
    vi.spyOn(apiClient, 'post').mockResolvedValueOnce(
      mockResponse({ id: 'cam_123', name: 'Front Gate Camera' })
    )

    const res = await gb28181Api.addChannelAsCamera(
      '34020000001320000001',
      '34020000001310000001'
    )

    expect(res).toEqual({ id: 'cam_123', name: 'Front Gate Camera' })
    expect(apiClient.post).toHaveBeenCalledWith(
      '/gb28181/devices/34020000001320000001/channels/34020000001310000001/camera',
      {}
    )
  })

  it('addChannelAsCamera rejects malformed response payloads', async () => {
    vi.spyOn(apiClient, 'post').mockResolvedValueOnce(
      mockResponse({ unexpected: true })
    )

    await expect(
      gb28181Api.addChannelAsCamera(
        '34020000001320000001',
        '34020000001310000001'
      )
    ).rejects.toThrow()
  })

  it('deleteDevice deletes the device', async () => {
    vi.spyOn(apiClient, 'delete').mockResolvedValueOnce(
      mockResponse({ deleted: true })
    )

    await gb28181Api.deleteDevice('34020000001320000001')

    expect(apiClient.delete).toHaveBeenCalledWith(
      '/gb28181/devices/34020000001320000001'
    )
  })
})
