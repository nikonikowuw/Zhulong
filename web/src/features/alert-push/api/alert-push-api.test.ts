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

const baseWebhookResponse = {
  id: 'dest_01',
  name: 'Webhook 1',
  channelType: 'webhook' as const,
  enabled: true,
  eventsEnabled: true,
  heartbeatEnabled: false,
  imageMode: 'url' as const,
  config: {
    url: 'https://example.com/alerts',
    authType: 'none' as const,
    timeoutSeconds: 5,
  },
  filter: {
    eventTypes: [],
    algorithmIds: ['algo_1'],
    cameraIds: [],
  },
  secretSet: false,
  signingSecretSet: true,
  headerNames: [],
  status: {
    consecutiveFailures: 0,
    totalDispatched: 10,
    queueLength: 0,
    queueCapacity: 32,
    state: 'idle' as const,
  },
}

describe('alertPushApi', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('list() returns parsed destinations including signingSecretSet', async () => {
    vi.mocked(apiClient.get).mockResolvedValueOnce({
      data: {
        code: 'OK',
        data: {
          items: [baseWebhookResponse],
        },
      },
    })

    const res = await alertPushApi.list()
    expect(res).toHaveLength(1)
    expect(res[0].id).toBe('dest_01')
    expect(res[0].name).toBe('Webhook 1')
    expect(res[0].signingSecretSet).toBe(true)
    expect(apiClient.get).toHaveBeenCalledWith('/alert-destinations')
  })

  it('create() posts input and returns created destination with default signingSecretSet=false', async () => {
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
    expect(res.signingSecretSet).toBe(false)
    expect(apiClient.post).toHaveBeenCalledWith(
      '/alert-destinations',
      expect.objectContaining({ name: 'MQTT 1' })
    )
  })

  it('update() puts payload and returns parsed destination', async () => {
    vi.mocked(apiClient.put).mockResolvedValueOnce({
      data: {
        code: 'OK',
        data: {
          ...baseWebhookResponse,
          name: 'Webhook Renamed',
          signingSecretSet: false,
        },
      },
    })

    const res = await alertPushApi.update('dest_01', {
      name: 'Webhook Renamed',
      secret: { signingSecret: '' },
    })

    expect(res.name).toBe('Webhook Renamed')
    expect(res.signingSecretSet).toBe(false)
    expect(apiClient.put).toHaveBeenCalledWith('/alert-destinations/dest_01', {
      name: 'Webhook Renamed',
      secret: { signingSecret: '' },
    })
  })

  it('enable() and disable() post to lifecycle endpoints and parse updated destination', async () => {
    vi.mocked(apiClient.post)
      .mockResolvedValueOnce({
        data: {
          code: 'OK',
          data: { ...baseWebhookResponse, enabled: true },
        },
      })
      .mockResolvedValueOnce({
        data: {
          code: 'OK',
          data: { ...baseWebhookResponse, enabled: false },
        },
      })

    const enabled = await alertPushApi.enable('dest_01')
    expect(enabled.enabled).toBe(true)
    expect(apiClient.post).toHaveBeenNthCalledWith(
      1,
      '/alert-destinations/dest_01/enable'
    )

    const disabled = await alertPushApi.disable('dest_01')
    expect(disabled.enabled).toBe(false)
    expect(apiClient.post).toHaveBeenNthCalledWith(
      2,
      '/alert-destinations/dest_01/disable'
    )
  })

  it('delete() validates OK envelope and rejects non-OK response', async () => {
    vi.mocked(apiClient.delete).mockResolvedValueOnce({
      data: { code: 'OK', data: null },
    })
    await alertPushApi.delete('dest_01')
    expect(apiClient.delete).toHaveBeenCalledWith('/alert-destinations/dest_01')

    vi.mocked(apiClient.delete).mockResolvedValueOnce({
      data: { code: 'INTERNAL_ERROR', data: null },
    })
    await expect(alertPushApi.delete('dest_01')).rejects.toThrow()
  })

  it('test() posts test payload with channelType and returns result', async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({
      data: {
        code: 'OK',
        data: {
          ok: true,
          latencyMs: 45,
        },
      },
    })

    const res = await alertPushApi.test({
      id: 'dest_01',
      channelType: 'webhook',
    })
    expect(res.ok).toBe(true)
    expect(res.latencyMs).toBe(45)
    expect(apiClient.post).toHaveBeenCalledWith('/alert-destinations/test', {
      id: 'dest_01',
      channelType: 'webhook',
    })
  })

  it('normalizes Go backend AlertDestinationStatus and capability flags', async () => {
    vi.mocked(apiClient.get).mockResolvedValueOnce({
      data: {
        code: 'OK',
        data: {
          items: [
            {
              id: 'dest_go_01',
              name: 'Heartbeat Only Go Payload',
              channelType: 'webhook',
              enabled: true,
              eventsEnabled: false,
              heartbeatEnabled: true,
              imageMode: 'url',
              config: {
                url: 'https://ops.example.com/events',
                heartbeatUrl: 'https://ops.example.com/heartbeat',
                authType: 'bearer',
                timeoutSeconds: 5,
                heartbeatIntervalSeconds: 30,
              },
              filter: {
                eventTypes: [],
                algorithmIds: [],
                cameraIds: [],
              },
              secretSet: true,
              status: {
                state: 'connected',
                lastError: '',
                lastSuccessAt: '2026-11-24T09:59:00Z',
                totalDispatched: 42,
                dropped: 0,
                queued: 0,
              },
            },
          ],
        },
      },
    })

    const res = await alertPushApi.list()
    expect(res).toHaveLength(1)
    expect(res[0].eventsEnabled).toBe(false)
    expect(res[0].heartbeatEnabled).toBe(true)
    expect(res[0].status.state).toBe('running')
    expect(res[0].status.queueLength).toBe(0)
    expect(res[0].status.dropped).toBe(0)
    expect(res[0].status.totalDispatched).toBe(42)
    expect(res[0].status.lastSuccessAt).toBe('2026-11-24T09:59:00Z')
  })

  it('preserves connecting state, failure telemetry, and dropped counts from Go status payload', async () => {
    vi.mocked(apiClient.get).mockResolvedValueOnce({
      data: {
        code: 'OK',
        data: {
          items: [
            {
              id: 'dest_mqtt_conn',
              name: 'Connecting MQTT',
              channelType: 'mqtt',
              enabled: true,
              eventsEnabled: true,
              heartbeatEnabled: false,
              imageMode: 'base64',
              config: {
                broker: 'tcp://10.0.0.1:1883',
                topic: 'zhulong/events',
              },
              filter: { eventTypes: [], algorithmIds: [], cameraIds: [] },
              secretSet: false,
              status: {
                state: 'connecting',
                lastError: '',
                lastAttemptAt: null,
                lastSuccessAt: null,
                lastFailureAt: null,
                consecutiveFailures: 0,
                dropped: 5,
                queued: 3,
              },
            },
            {
              id: 'dest_wh_fail',
              name: 'Failing Webhook',
              channelType: 'webhook',
              enabled: true,
              eventsEnabled: true,
              heartbeatEnabled: false,
              imageMode: 'multipart',
              config: {
                url: 'https://ops.example.com/hook',
                authType: 'none',
              },
              filter: { eventTypes: [], algorithmIds: [], cameraIds: [] },
              secretSet: false,
              status: {
                state: 'failed',
                lastError: 'timeout',
                lastAttemptAt: '2026-11-24T10:00:00Z',
                lastSuccessAt: null,
                lastFailureAt: '2026-11-24T10:00:00Z',
                consecutiveFailures: 4,
                dropped: 2,
                queued: 16,
              },
            },
          ],
        },
      },
    })

    const res = await alertPushApi.list()
    expect(res).toHaveLength(2)
    expect(res[0].status.state).toBe('connecting')
    expect(res[0].status.dropped).toBe(5)
    expect(res[0].status.queueLength).toBe(3)

    expect(res[1].status.state).toBe('degraded')
    expect(res[1].status.lastAttemptAt).toBe('2026-11-24T10:00:00Z')
    expect(res[1].status.lastFailureAt).toBe('2026-11-24T10:00:00Z')
    expect(res[1].status.lastFailureReason).toBe('timeout')
    expect(res[1].status.consecutiveFailures).toBe(4)
    expect(res[1].status.dropped).toBe(2)
  })
})
