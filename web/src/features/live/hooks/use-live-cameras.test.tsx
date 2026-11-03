import type { AxiosResponse } from 'axios'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { renderHook } from 'vitest-browser-react'
import { apiClient } from '@/lib/api-client'
import { type CameraListResponse } from '@/features/cameras/contract'
import { useLiveCameras } from './use-live-cameras'

type SseListener = (event: MessageEvent) => void

/**
 * 最小 EventSource 桩：只实现 `useCameraEvents` 实际用到的成员，
 * 并暴露 `emit` 让我们主动推送 snapshot / change 事件。
 */
class MockEventSource {
  static instances: MockEventSource[] = []

  readonly url: string
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: SseListener | null = null
  closed = false
  private listeners = new Map<string, SseListener[]>()

  constructor(url: string) {
    this.url = url
    MockEventSource.instances.push(this)
  }

  addEventListener(type: string, listener: SseListener) {
    const bucket = this.listeners.get(type) ?? []
    bucket.push(listener)
    this.listeners.set(type, bucket)
  }

  close() {
    this.closed = true
  }

  emit(type: string, payload: unknown) {
    const event = new MessageEvent(type, { data: JSON.stringify(payload) })
    for (const listener of this.listeners.get(type) ?? []) {
      listener(event)
    }
  }
}

function listResponse(items: CameraListResponse['items']): CameraListResponse {
  return { items, total: items.length, page: 1, pageSize: 100 }
}

function camera(
  overrides: Partial<CameraListResponse['items'][number]> = {}
): CameraListResponse['items'][number] {
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
    streams: [
      {
        id: 1,
        role: 'main',
        protocol: 'rtsp',
        rtspUrl: 'rtsp://admin:pass@192.168.1.10:554/live',
        host: '192.168.1.10',
        transport: 'tcp',
        codec: 'H264',
        width: 1920,
        height: 1080,
        fps: 25,
        fpsString: '25',
      },
    ],
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function mockCameras(payload: CameraListResponse) {
  vi.spyOn(apiClient, 'get').mockResolvedValue({
    data: { code: 'OK', message: 'success', data: payload },
    status: 200,
    statusText: 'OK',
    headers: {},
    config: {} as AxiosResponse['config'],
  })
}

function renderLiveCameras() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const wrapper = ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
  return renderHook(() => useLiveCameras(), { wrapper })
}

describe('useLiveCameras', () => {
  const originalEventSource = globalThis.EventSource

  beforeEach(() => {
    MockEventSource.instances = []
    globalThis.EventSource = MockEventSource as unknown as typeof EventSource
  })

  afterEach(() => {
    vi.restoreAllMocks()
    globalThis.EventSource = originalEventSource
  })

  it('exposes the adapted display model from the shared camera contract', async () => {
    mockCameras(listResponse([camera()]))

    const { result } = await renderLiveCameras()

    await vi.waitFor(() => {
      expect(result.current.data).toHaveLength(1)
    })
    expect(result.current.data[0]).toMatchObject({
      id: 'cam-01',
      host: '192.168.1.10',
      resolution: '1920x1080',
      codec: 'h264',
      status: 'online',
    })
  })

  it('opens the camera SSE channel instead of polling', async () => {
    mockCameras(listResponse([camera()]))

    await renderLiveCameras()

    await vi.waitFor(() => {
      expect(MockEventSource.instances).toHaveLength(1)
    })
    expect(MockEventSource.instances[0].url).toBe('/api/v1/cameras/events')
  })

  // 后端 `snapshot` 载荷是 { cameraId: state } 的对象，不是数组。
  // 按数组解析会让每一条快照被静默丢弃：重连后补不到断连期间的状态变化，
  // 任务页永久停在断线前那一帧（「一直显示视频流重连中」）。
  it('applies the reconnect snapshot delivered as a cameraId-keyed map', async () => {
    mockCameras(
      listResponse([camera({ health: 'error', session: 'reconnecting' })])
    )

    const { result } = await renderLiveCameras()
    await vi.waitFor(() => {
      expect(result.current.data[0]?.status).toBe('error')
    })

    MockEventSource.instances[0].emit('snapshot', {
      'cam-01': {
        cameraId: 'cam-01',
        enabled: true,
        revision: 1,
        health: 'online',
        session: 'running',
        degraded: false,
        stale: false,
        reason: '',
      },
    })

    await vi.waitFor(() => {
      expect(result.current.data[0]?.status).toBe('online')
    })
  })

  it('patches status from an SSE change event without refetching', async () => {
    mockCameras(listResponse([camera()]))

    const { result } = await renderLiveCameras()
    await vi.waitFor(() => {
      expect(result.current.data[0]?.status).toBe('online')
    })

    const getSpy = vi.mocked(apiClient.get)
    const callsAfterInitialFetch = getSpy.mock.calls.length

    MockEventSource.instances[0].emit('change', {
      cameraId: 'cam-01',
      enabled: true,
      revision: 1,
      health: 'error',
      session: 'error',
      degraded: false,
      stale: false,
      reason: 'rtsp timeout',
    })

    await vi.waitFor(() => {
      expect(result.current.data[0]?.status).toBe('error')
    })
    // 关键：状态来自 SSE 增量，而不是又打了一次 /cameras
    expect(getSpy.mock.calls.length).toBe(callsAfterInitialFetch)
  })
})
