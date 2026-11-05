import { describe, it, expect, vi, beforeEach } from 'vitest'
import { apiClient } from '@/lib/api-client'
import { alertPushApi } from './alert-push-api'

vi.mock('@/lib/api-client', () => ({
  apiClient: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}))

describe('alertPushApi', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('list() returns parsed destinations', async () => {
    const mockData = {
      code: 'OK',
      data: {
        items: [
          {
            id: 'dest_01',
            name: 'Webhook 1',
            channelType: 'webhook',
            enabled: true,
            imageMode: 'url',
            config: {
              url: 'https://example.com/alerts',
              authType: 'none',
              timeoutSeconds: 5,
            },
            filter: {
              algorithmIds: ['algo_1'],
              cameraIds: [],
            },
            secretSet: false,
            headerNames: [],
            status: {
              consecutiveFailures: 0,
              totalDispatched: 10,
              queueLength: 0,
              queueCapacity: 32,
              state: 'idle',
            },
          },
        ],
      },
    }

    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: mockData })

    const res = await alertPushApi.list()
    expect(res).toHaveLength(1)
    expect(res[0].id).toBe('dest_01')
    expect(res[0].name).toBe('Webhook 1')
    expect(apiClient.get).toHaveBeenCalledWith('/alert-destinations')
  })

  it('create() posts input and returns created destination', async () => {
    const mockCreated = {
      code: 'OK',
      data: {
        id: 'dest_02',
        name: 'MQTT 1',
        channelType: 'mqtt',
        enabled: true,
        imageMode: 'base64',
        config: {
          broker: 'tcp://127.0.0.1:1883',
          topic: 'events',
          qos: 1,
          tlsSkipVerify: false,
        },
        filter: {
          algorithmIds: [],
          cameraIds: [],
        },
        secretSet: true,
        status: {
          consecutiveFailures: 0,
          totalDispatched: 0,
          queueLength: 0,
          queueCapacity: 32,
          state: 'idle',
        },
      },
    }

    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: mockCreated })

    const res = await alertPushApi.create({
      name: 'MQTT 1',
      channelType: 'mqtt',
      imageMode: 'base64',
      config: { broker: 'tcp://127.0.0.1:1883', topic: 'events', qos: 1 },
      secret: { password: 'secret' },
    })

    expect(res.id).toBe('dest_02')
    expect(apiClient.post).toHaveBeenCalledWith(
      '/alert-destinations',
      expect.objectContaining({ name: 'MQTT 1' })
    )
  })

  it('delete() calls DELETE endpoint', async () => {
    vi.mocked(apiClient.delete).mockResolvedValueOnce({
      data: { code: 'OK', data: null },
    })
    await alertPushApi.delete('dest_01')
    expect(apiClient.delete).toHaveBeenCalledWith('/alert-destinations/dest_01')
  })

  it('test() posts test payload and returns result', async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({
      data: {
        code: 'OK',
        data: {
          ok: true,
          latencyMs: 45,
        },
      },
    })

    const res = await alertPushApi.test({ id: 'dest_01' })
    expect(res.ok).toBe(true)
    expect(res.latencyMs).toBe(45)
    expect(apiClient.post).toHaveBeenCalledWith('/alert-destinations/test', {
      id: 'dest_01',
    })
  })
})
