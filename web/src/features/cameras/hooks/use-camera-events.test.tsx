import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { renderHook } from 'vitest-browser-react'
import { type Camera, type CameraListResponse } from '../data/schema'
import { useCameraEvents } from './use-camera-events'
import { CAMERAS_QUERY_KEY } from './use-cameras'

type SseListener = (event: MessageEvent) => void

class MockEventSource {
  static instances: MockEventSource[] = []

  readonly url: string
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
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

function createCamera(id: string, name: string): Camera {
  return {
    id,
    name,
    enabled: true,
    revision: 1,
    health: 'unknown',
    session: 'idle',
    degraded: false,
    stale: false,
    reason: '',
    streams: [],
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
  }
}

describe('useCameraEvents', () => {
  const originalEventSource = globalThis.EventSource

  beforeEach(() => {
    MockEventSource.instances = []
    globalThis.EventSource = MockEventSource as unknown as typeof EventSource
  })

  afterEach(() => {
    vi.restoreAllMocks()
    globalThis.EventSource = originalEventSource
  })

  it('shares a single EventSource connection across multiple hook subscribers', async () => {
    const queryClient = new QueryClient()
    const wrapper = ({ children }: { children: React.ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )

    const hook1 = await renderHook(() => useCameraEvents(true), { wrapper })
    const hook2 = await renderHook(() => useCameraEvents(true), { wrapper })

    await vi.waitFor(() => {
      expect(MockEventSource.instances).toHaveLength(1)
    })
    expect(MockEventSource.instances[0].closed).toBe(false)

    await hook1.unmount()
    // Still 1 subscriber alive, connection stays open
    expect(MockEventSource.instances[0].closed).toBe(false)

    await hook2.unmount()
    // All subscribers gone, connection closes
    expect(MockEventSource.instances[0].closed).toBe(true)
  })

  it('patches both list cache and detail cache on change event', async () => {
    const queryClient = new QueryClient()
    const wrapper = ({ children }: { children: React.ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )

    const initialCamera = createCamera('cam-01', 'Front Gate')
    queryClient.setQueryData<CameraListResponse>(
      [...CAMERAS_QUERY_KEY, 'all'],
      { items: [initialCamera], total: 1, page: 1, pageSize: 100 }
    )
    queryClient.setQueryData<Camera>(
      [...CAMERAS_QUERY_KEY, 'detail', 'cam-01'],
      initialCamera
    )

    const hook = await renderHook(() => useCameraEvents(true), { wrapper })

    await vi.waitFor(() => {
      expect(MockEventSource.instances).toHaveLength(1)
    })

    MockEventSource.instances[0].emit('change', {
      cameraId: 'cam-01',
      enabled: true,
      revision: 2,
      health: 'online',
      session: 'running',
      degraded: false,
      stale: false,
    })

    await vi.waitFor(() => {
      const listData = queryClient.getQueryData<CameraListResponse>([
        ...CAMERAS_QUERY_KEY,
        'all',
      ])
      const detailData = queryClient.getQueryData<Camera>([
        ...CAMERAS_QUERY_KEY,
        'detail',
        'cam-01',
      ])

      expect(listData?.items[0].health).toBe('online')
      expect(listData?.items[0].session).toBe('running')
      expect(detailData?.health).toBe('online')
      expect(detailData?.session).toBe('running')
    })

    await hook.unmount()
  })
})
