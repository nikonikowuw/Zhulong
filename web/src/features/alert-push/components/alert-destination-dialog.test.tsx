import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { alertPushApi } from '../api/alert-push-api'
import type { AlertDestination } from '../types'
import { AlertDestinationDialog } from './alert-destination-dialog'
import { AlertDestinationsTable } from './alert-destinations-table'

vi.mock('../api/alert-push-api', () => ({
  alertPushApi: {
    create: vi.fn().mockResolvedValue({ id: 'dest-01' }),
    update: vi.fn().mockResolvedValue({ id: 'dest-01' }),
    test: vi.fn().mockResolvedValue({ ok: true, latencyMs: 24 }),
  },
}))

vi.mock('@/features/algorithms/api/algorithms-api', () => ({
  algorithmsApi: {
    getAlgorithms: vi.fn().mockResolvedValue([
      { id: 'algo-1', name: 'Intrusion Detection' },
      { id: 'algo-2', name: 'Fire & Smoke' },
    ]),
  },
}))

vi.mock('@/features/cameras/api/cameras-api', () => ({
  camerasApi: {
    getAll: vi.fn().mockResolvedValue({
      items: [
        { id: 'cam-1', name: 'Gate Camera' },
        { id: 'cam-2', name: 'Lobby Camera' },
      ],
      total: 2,
    }),
  },
}))

const mockDestination: AlertDestination = {
  id: 'dest-100',
  name: 'Existing Webhook',
  channelType: 'webhook',
  enabled: true,
  eventsEnabled: true,
  heartbeatEnabled: true,
  imageMode: 'multipart',
  config: {
    url: 'https://api.example.com/alerts/webhook',
    heartbeatUrl: 'https://api.example.com/devices/heartbeat',
    authType: 'none',
    timeoutSeconds: 5,
    heartbeatIntervalSeconds: 45,
  },
  filter: {
    eventTypes: ['alarm'],
    algorithmIds: ['algo-1'],
    cameraIds: ['cam-1'],
  },
  secretSet: true,
  headerNames: ['X-Custom-Token'],
  status: {
    consecutiveFailures: 0,
    totalDispatched: 10,
    queueLength: 2,
    queueCapacity: 16,
    state: 'idle',
  },
}

describe('AlertDestinationDialog & Table Capabilities', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders create dialog with connection, event push, and device liveness sections', async () => {
    const queryClient = new QueryClient()
    const { getByRole, getByPlaceholder, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertDestinationDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
        />
      </QueryClientProvider>
    )

    await expect
      .element(getByRole('heading', { name: /新建推送目标/i }))
      .toBeInTheDocument()

    await expect
      .element(getByText('HTTP Webhook', { exact: true }))
      .toBeInTheDocument()
    await expect
      .element(getByText('MQTT 消息总线', { exact: true }))
      .toBeInTheDocument()

    await expect
      .element(getByPlaceholder(/例如：SOC Webhook 回调/i))
      .toBeInTheDocument()
    await expect
      .element(getByPlaceholder(/https:\/\/api.example.com\/alerts\/webhook/i))
      .toBeInTheDocument()

    // Both capability section headers are rendered
    await expect
      .element(getByText('感知事件推送', { exact: true }))
      .toBeInTheDocument()
    await expect
      .element(getByText('设备存活上报', { exact: true }))
      .toBeInTheDocument()
  })

  it('toggles event push and device liveness sections and blocks submit when both are disabled', async () => {
    const queryClient = new QueryClient()
    const { getByRole, getByText, getByPlaceholder } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertDestinationDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
        />
      </QueryClientProvider>
    )

    const nameInput = getByRole('textbox', { name: /通道名称/i })
    await userEvent.type(nameInput, 'Park Platform')
    const urlInput = getByPlaceholder(
      /https:\/\/api.example.com\/alerts\/webhook/i
    )
    await userEvent.type(urlInput, 'https://api.example.com/events')

    // Disable Perception Event Push -> both capabilities are now off
    const eventsSwitch = getByRole('switch', { name: '感知事件推送' })
    await userEvent.click(eventsSwitch)

    // Image mode field is hidden when eventsEnabled is false
    await expect.element(getByText('图片附带模式')).not.toBeInTheDocument()

    // Submitting with both capabilities off shows validation error and does not call API
    const saveBtn = getByRole('button', { name: /保存目标/i })
    await userEvent.click(saveBtn)

    await expect
      .element(
        getByText(/请至少开启“感知事件推送”或“设备存活上报”中的一项能力/)
      )
      .toBeInTheDocument()
    expect(alertPushApi.create).not.toHaveBeenCalled()

    // Enable Device Liveness Reporting -> reveals reporting interval & optional heartbeat URL
    const heartbeatSwitch = getByRole('switch', { name: '设备存活上报' })
    await userEvent.click(heartbeatSwitch)

    await expect.element(getByText(/上报间隔/)).toBeInTheDocument()
    await expect.element(getByText(/独立存活接收 URL/)).toBeInTheDocument()
  })

  it('seamlessly switches protocol fields and MQTT LWT options when MQTT card is selected', async () => {
    const queryClient = new QueryClient()
    const { getByRole, getByText, getByPlaceholder } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertDestinationDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
        />
      </QueryClientProvider>
    )

    await userEvent.click(getByText('MQTT 消息总线', { exact: true }))

    await expect
      .element(getByPlaceholder(/tcp:\/\/192.168.1.100:1883/i))
      .toBeInTheDocument()
    await expect
      .element(getByPlaceholder(/zhulong\/alarms/i))
      .toBeInTheDocument()

    await userEvent.click(getByRole('switch', { name: '设备存活上报' }))
    await expect.element(getByText(/独立状态 Topic/)).toBeInTheDocument()
    await expect
      .element(getByRole('switch', { name: /断线遗嘱通知/ }))
      .toBeChecked()
  })

  it('executes connection probe test directly in the footer without context switching', async () => {
    const queryClient = new QueryClient()
    const { getByRole, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertDestinationDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
        />
      </QueryClientProvider>
    )

    const testBtn = getByRole('button', { name: /测试连通性/i })
    await userEvent.click(testBtn)

    await vi.waitFor(() => {
      expect(alertPushApi.test).toHaveBeenCalledTimes(1)
    })

    await expect.element(getByText(/探测成功 \(24ms\)/i)).toBeInTheDocument()
  })

  it('populates fields and locks protocol type in edit mode', async () => {
    const queryClient = new QueryClient()
    const { getByRole, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertDestinationDialog
          open={true}
          destination={mockDestination}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
        />
      </QueryClientProvider>
    )

    await expect
      .element(getByRole('heading', { name: /编辑推送目标/i }))
      .toBeInTheDocument()

    const nameInput = getByRole('textbox', { name: /通道名称/i })
    await expect.element(nameInput).toHaveValue('Existing Webhook')

    await expect
      .element(getByText(/传输协议类型创建后固定/i))
      .toBeInTheDocument()

    const radios = getByRole('radio').all()
    expect(radios).toHaveLength(2)
    await expect.element(radios[0]).toBeDisabled()
    await expect.element(radios[1]).toBeDisabled()
  })

  it('sends undefined for untouched headers and empty object {} when clearing all headers in edit mode', async () => {
    const queryClient = new QueryClient()
    const onSuccess = vi.fn()
    const { getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertDestinationDialog
          open={true}
          destination={mockDestination}
          onOpenChange={vi.fn()}
          onSuccess={onSuccess}
        />
      </QueryClientProvider>
    )

    // 1. Submit immediately without touching headers -> secret.headers must be undefined (retain on server)
    await userEvent.click(getByRole('button', { name: /保存目标/i }))

    await vi.waitFor(() => {
      expect(alertPushApi.update).toHaveBeenCalledTimes(1)
    })
    expect(alertPushApi.update).toHaveBeenLastCalledWith(
      'dest-100',
      expect.objectContaining({
        secret: expect.objectContaining({ headers: undefined }),
      })
    )

    // 2. Remove existing header -> secret.headers must be {} (clear on server)
    await userEvent.click(getByRole('button', { name: '移除标头' }))
    await userEvent.click(getByRole('button', { name: /保存目标/i }))

    await vi.waitFor(() => {
      expect(alertPushApi.update).toHaveBeenCalledTimes(2)
    })
    expect(alertPushApi.update).toHaveBeenLastCalledWith(
      'dest-100',
      expect.objectContaining({
        secret: expect.objectContaining({ headers: {} }),
      })
    )
  })

  it('renders error banner when submit throws non-Axios error', async () => {
    vi.mocked(alertPushApi.create).mockRejectedValueOnce(
      new Error('Unexpected response schema')
    )
    const queryClient = new QueryClient()
    const { getByRole, getByPlaceholder, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertDestinationDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
        />
      </QueryClientProvider>
    )

    await userEvent.type(
      getByRole('textbox', { name: /通道名称/i }),
      'New Webhook'
    )
    await userEvent.type(
      getByPlaceholder(/https:\/\/api.example.com\/alerts\/webhook/i),
      'https://api.example.com/hook'
    )
    await userEvent.click(getByRole('button', { name: /保存目标/i }))

    await expect
      .element(getByText('Unexpected response schema'))
      .toBeInTheDocument()
  })

  it('renders capability badges and em-dash queue cell for heartbeat-only rows in table', async () => {
    const heartbeatOnlyDest: AlertDestination = {
      ...mockDestination,
      id: 'dest-hb-only',
      name: 'Cloud Ops Monitor',
      eventsEnabled: false,
      heartbeatEnabled: true,
      config: {
        url: 'https://ops.example.com/heartbeat',
        authType: 'none',
        heartbeatIntervalSeconds: 30,
      },
    }

    const { getByText } = await render(
      <AlertDestinationsTable
        destinations={[mockDestination, heartbeatOnlyDest]}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
        onToggle={vi.fn()}
        onTest={vi.fn()}
      />
    )

    await expect.element(getByText('事件推送')).toBeInTheDocument()
    await expect.element(getByText('存活上报 · 45s')).toBeInTheDocument()
    await expect.element(getByText('存活上报 · 30s')).toBeInTheDocument()
    await expect.element(getByText('2 / 16')).toBeInTheDocument()
  })
})
