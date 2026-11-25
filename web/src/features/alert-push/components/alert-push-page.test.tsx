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

vi.mock('@/features/algorithms/contract', () => ({
  useAlgorithms: vi.fn(() => ({
    data: [],
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  })),
}))

vi.mock('@/features/cameras/contract', () => ({
  useAllCameras: vi.fn(() => ({
    data: { items: [], total: 0 },
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  })),
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
    url: 'https://soc.example.com/hook?access_token=secret123',
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
  signingSecretSet: false,
  status: {
    consecutiveFailures: 0,
    totalDispatched: 5,
    dropped: 0,
    queueLength: 0,
    queueCapacity: 16,
    lastAttemptAt: '2026-11-24T10:00:00Z',
    lastSuccessAt: '2026-11-24T10:00:00Z',
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

  it('handles quick test with channelType, masks webhook query params, and deletes with confirmation', async () => {
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
    // Webhook URL query string is masked in table subtitle
    await expect
      .element(getByText('https://soc.example.com/hook?...'))
      .toBeInTheDocument()
    // Dispatched count telemetry is rendered
    await expect.element(getByText('累计 5 条')).toBeInTheDocument()

    // Open row actions -> Test Connection (must include channelType for audit target)
    await userEvent.click(getByRole('button', { name: '操作' }))
    await userEvent.click(getByRole('menuitem', { name: '测试连通性' }))

    expect(alertPushApi.test).toHaveBeenCalledWith({
      id: 'dest-1',
      channelType: 'webhook',
    })
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

  it('disables toggle switch while enable/disable mutation is pending', async () => {
    let resolveDisable!: (dest: AlertDestination) => void
    const pendingDisable = new Promise<AlertDestination>((resolve) => {
      resolveDisable = resolve
    })

    vi.mocked(alertPushApi.list).mockResolvedValue([sampleDest])
    vi.mocked(alertPushApi.disable).mockReturnValueOnce(pendingDisable)

    const queryClient = createTestQueryClient()
    const { getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertPushPage />
      </QueryClientProvider>
    )

    const toggleSwitch = getByRole('switch', { name: 'SOC Webhook' })
    await expect.element(toggleSwitch).toBeEnabled()

    await userEvent.click(toggleSwitch)

    await expect.element(toggleSwitch).toBeDisabled()
    expect(alertPushApi.disable).toHaveBeenCalledWith('dest-1')

    resolveDisable({ ...sampleDest, enabled: false })
    await expect.element(toggleSwitch).toBeEnabled()
  })
})
