import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { alertPushApi } from '../api/alert-push-api'
import type { AlertDestination } from '../types'
import { AlertPushPage } from './alert-push-page'

vi.mock('../api/alert-push-api', () => ({
  alertPushApi: {
    list: vi.fn(),
    create: vi.fn(),
    update: vi.fn(),
    delete: vi.fn(),
    enable: vi.fn(),
    disable: vi.fn(),
    test: vi.fn(),
  },
}))

vi.mock('@/features/algorithms/api/algorithms-api', () => ({
  algorithmsApi: {
    getAlgorithms: vi.fn().mockResolvedValue([]),
  },
}))

vi.mock('@/features/cameras/api/cameras-api', () => ({
  camerasApi: {
    getAll: vi.fn().mockResolvedValue({ items: [], total: 0 }),
  },
}))

const sampleDest: AlertDestination = {
  id: 'dest-1',
  name: 'SOC Webhook',
  channelType: 'webhook',
  enabled: true,
  eventsEnabled: true,
  heartbeatEnabled: false,
  imageMode: 'multipart',
  config: {
    url: 'https://soc.example.com/hook',
    authType: 'none',
    timeoutSeconds: 5,
    heartbeatIntervalSeconds: 60,
  },
  filter: {
    eventTypes: ['alarm'],
    algorithmIds: [],
    cameraIds: [],
  },
  secretSet: false,
  status: {
    consecutiveFailures: 0,
    totalDispatched: 5,
    queueLength: 0,
    queueCapacity: 16,
    state: 'running',
  },
}

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  })
}

describe('AlertPushPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders error state with retry button without showing empty state on initial fetch failure', async () => {
    vi.mocked(alertPushApi.list)
      .mockRejectedValueOnce(new Error('Network unavailable'))
      .mockResolvedValueOnce([sampleDest])

    const queryClient = createTestQueryClient()
    const { getByText, getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertPushPage />
      </QueryClientProvider>
    )

    await expect.element(getByText('Network unavailable')).toBeInTheDocument()
    await expect
      .element(getByText('暂无配置告警推送目标'))
      .not.toBeInTheDocument()

    await userEvent.click(getByRole('button', { name: '重试' }))

    await expect.element(getByText('SOC Webhook')).toBeInTheDocument()
  })

  it('renders empty state when no destinations are configured', async () => {
    vi.mocked(alertPushApi.list).mockResolvedValueOnce([])

    const queryClient = createTestQueryClient()
    const { getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertPushPage />
      </QueryClientProvider>
    )

    await expect.element(getByText('暂无配置告警推送目标')).toBeInTheDocument()
  })

  it('handles delete confirmation dialog with localized actions and quick test flow', async () => {
    vi.mocked(alertPushApi.list).mockResolvedValue([sampleDest])
    vi.mocked(alertPushApi.delete).mockResolvedValueOnce(undefined)
    vi.mocked(alertPushApi.test).mockResolvedValueOnce({
      ok: true,
      latencyMs: 18,
    })

    const queryClient = createTestQueryClient()
    const { getByRole, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertPushPage />
      </QueryClientProvider>
    )

    await expect.element(getByText('SOC Webhook')).toBeInTheDocument()

    // Open row actions -> Test Connection
    await userEvent.click(getByRole('button', { name: '操作' }))
    await userEvent.click(getByRole('menuitem', { name: '测试连通性' }))

    await expect.element(getByText('探测成功 (18ms)')).toBeInTheDocument()
    await userEvent.click(getByRole('button', { name: '关闭' }))

    // Open row actions -> Delete Destination
    await userEvent.click(getByRole('button', { name: '操作' }))
    await userEvent.click(getByRole('menuitem', { name: '删除目标' }))

    await expect.element(getByText('确认删除推送目标？')).toBeInTheDocument()
    await expect
      .element(getByRole('button', { name: '取消' }))
      .toBeInTheDocument()

    await userEvent.click(getByRole('button', { name: '删除目标' }))

    await vi.waitFor(() => {
      expect(alertPushApi.delete).toHaveBeenCalledWith('dest-1')
    })
  })
})
