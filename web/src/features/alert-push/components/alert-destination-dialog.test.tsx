import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { alertPushApi } from '../api/alert-push-api'
import type { AlertDestination } from '../types'
import { AlertDestinationDialog } from './alert-destination-dialog'

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
    getCameras: vi.fn().mockResolvedValue({
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
  imageMode: 'multipart',
  config: {
    url: 'https://api.example.com/alerts/webhook',
    authType: 'none',
    timeoutSeconds: 5,
    broker: '',
    topic: '',
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
    queueLength: 0,
    queueCapacity: 32,
    state: 'idle',
  },
}

describe('AlertDestinationDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders create dialog in a coherent single-flow layout without tab switching', async () => {
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

    // Heading
    await expect
      .element(getByRole('heading', { name: /新建推送目标/i }))
      .toBeInTheDocument()

    // Protocol card titles (exact match)
    await expect
      .element(getByText('HTTP Webhook', { exact: true }))
      .toBeInTheDocument()
    await expect
      .element(getByText('MQTT 消息总线', { exact: true }))
      .toBeInTheDocument()

    // Name input
    await expect
      .element(getByPlaceholder(/例如：SOC Webhook 回调/i))
      .toBeInTheDocument()

    // Webhook URL is directly visible in the main flow (zero tab switching!)
    await expect
      .element(getByPlaceholder(/https:\/\/api.example.com\/alerts\/webhook/i))
      .toBeInTheDocument()
  })

  it('seamlessly switches protocol fields when MQTT card is selected', async () => {
    const queryClient = new QueryClient()
    const { getByText, getByPlaceholder } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertDestinationDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
        />
      </QueryClientProvider>
    )

    // Select MQTT protocol card
    const mqttCard = getByText('MQTT 消息总线', { exact: true })
    await userEvent.click(mqttCard)

    // MQTT Broker & Topic fields are now in place directly
    await expect
      .element(getByPlaceholder(/tcp:\/\/192.168.1.100:1883/i))
      .toBeInTheDocument()
    await expect
      .element(getByPlaceholder(/zhulong\/alarms/i))
      .toBeInTheDocument()
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

    // Probe success badge is displayed
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

    // Filled name via role textbox
    const nameInput = getByRole('textbox', { name: /通道名称/i })
    await expect.element(nameInput).toHaveValue('Existing Webhook')

    // Protocol locked notice
    await expect
      .element(getByText(/传输协议类型创建后固定/i))
      .toBeInTheDocument()
  })
})
