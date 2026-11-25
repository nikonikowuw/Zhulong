import { AxiosError, AxiosHeaders } from 'axios'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { useAlgorithms } from '@/features/algorithms/contract'
import { useAllCameras } from '@/features/cameras/contract'
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

vi.mock('@/features/algorithms/contract', () => ({
  useAlgorithms: vi.fn(),
}))

vi.mock('@/features/cameras/contract', () => ({
  useAllCameras: vi.fn(),
}))

function resetFilterOptionMocks() {
  vi.mocked(useAlgorithms).mockReturnValue({
    data: [
      { id: 'algo-1', name: 'Intrusion Detection' },
      { id: 'algo-2', name: 'Fire & Smoke' },
    ],
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  } as unknown as ReturnType<typeof useAlgorithms>)

  vi.mocked(useAllCameras).mockReturnValue({
    data: {
      items: [
        { id: 'cam-1', name: 'Gate Camera' },
        { id: 'cam-2', name: 'Lobby Camera' },
      ],
      total: 2,
    },
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  } as unknown as ReturnType<typeof useAllCameras>)
}

function makeAxiosError(status: number, data: unknown) {
  const headers = new AxiosHeaders()
  return new AxiosError(
    'Request failed',
    'ERR_BAD_REQUEST',
    { headers },
    undefined,
    {
      status,
      statusText: 'Error',
      headers,
      config: { headers },
      data,
    }
  )
}

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
  signingSecretSet: false,
  headerNames: ['X-Custom-Token'],
  status: {
    consecutiveFailures: 0,
    totalDispatched: 10,
    dropped: 0,
    queueLength: 2,
    queueCapacity: 16,
    lastAttemptAt: '2026-11-24T10:00:00Z',
    lastSuccessAt: '2026-11-24T10:00:00Z',
    state: 'idle',
  },
}

// 取当前抽屉的三段结构（内容容器 / 滚动区 / 底栏），缺失即断言失败
function getDrawerParts() {
  const last = (selector: string) => {
    const nodes = document.querySelectorAll(selector)
    return nodes[nodes.length - 1]
  }
  const sheet = last('[data-slot="sheet-content"]')
  const scroll = last('[data-testid="alert-destination-scroll"]')
  const footer = last('[data-slot="sheet-footer"]')
  if (
    !(sheet instanceof HTMLElement) ||
    !(scroll instanceof HTMLElement) ||
    !(footer instanceof HTMLElement)
  ) {
    throw new Error('alert destination drawer structure is incomplete')
  }
  return { sheet, scroll, footer }
}

// 等待入场动画落位，再量几何，避免量到 slide-in 中间态
async function settledSheet(sheet: HTMLElement) {
  const viewportRight = () =>
    document.documentElement.getBoundingClientRect().right
  await vi.waitFor(() =>
    expect(
      Math.abs(sheet.getBoundingClientRect().right - viewportRight())
    ).toBeLessThanOrEqual(1)
  )
  return viewportRight
}

describe('AlertDestinationDialog & Table Capabilities', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    resetFilterOptionMocks()
  })

  it('renders create dialog with connection, event push, and device liveness sections without eager filter queries', async () => {
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

    // Filters section is collapsed initially -> do not fetch algorithms/cameras eagerly
    expect(useAlgorithms).not.toHaveBeenCalled()
    expect(useAllCameras).not.toHaveBeenCalled()

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

    // 抽屉形态：右侧 Sheet 承载表单，居中 Dialog 已不再出现
    expect(document.querySelector('[data-slot="dialog-content"]')).toBeNull()
    const { sheet, scroll, footer } = getDrawerParts()
    const viewportRight = await settledSheet(sheet)

    // 抽屉自带的关闭按钮（sr-only "Close"）常驻右上角，不依赖业务表单实现
    await expect
      .element(getByRole('button', { name: 'Close' }))
      .toBeInTheDocument()

    const sheetRect = sheet.getBoundingClientRect()
    expect(Math.round(sheetRect.top)).toBe(0)
    expect(Math.abs(sheetRect.left)).toBeLessThanOrEqual(1)
    expect(Math.abs(sheetRect.right - viewportRight())).toBeLessThanOrEqual(1)

    // 只有中部滚动区滚动，底栏固定在抽屉底边
    expect(getComputedStyle(scroll).overflowY).toBe('auto')
    expect(Math.round(footer.getBoundingClientRect().bottom)).toBe(
      Math.round(sheetRect.bottom)
    )
    expect(scroll.getBoundingClientRect().bottom).toBeLessThanOrEqual(
      footer.getBoundingClientRect().top + 1
    )
    expect(scroll.scrollWidth).toBeLessThanOrEqual(scroll.clientWidth + 1)

    // 滚动内容时标题与底栏都保持原位
    const header = document.querySelector(
      '[data-slot="sheet-header"]'
    ) as HTMLElement
    const headerTop = header.getBoundingClientRect().top
    const footerTop = footer.getBoundingClientRect().top
    scroll.scrollTop = 10_000
    expect(Math.round(footer.getBoundingClientRect().top)).toBe(
      Math.round(footerTop)
    )
    expect(Math.round(header.getBoundingClientRect().top)).toBe(
      Math.round(headerTop)
    )
    scroll.scrollTop = 0
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

  it('switches protocol fields, updates drawer header icon, and keeps new MQTT broker empty by default', async () => {
    const queryClient = new QueryClient()
    const { getByRole, getByText, getByPlaceholder, getByTestId } =
      await render(
        <QueryClientProvider client={queryClient}>
          <AlertDestinationDialog
            open={true}
            onOpenChange={vi.fn()}
            onSuccess={vi.fn()}
          />
        </QueryClientProvider>
      )

    await expect
      .element(getByTestId('alert-destination-header-icon'))
      .toHaveAttribute('data-channel-type', 'webhook')

    await userEvent.click(getByText('MQTT 消息总线', { exact: true }))

    await expect
      .element(getByTestId('alert-destination-header-icon'))
      .toHaveAttribute('data-channel-type', 'mqtt')

    const brokerInput = getByPlaceholder(/tcp:\/\/192.168.1.100:1883/i)
    await expect.element(brokerInput).toHaveValue('')
    await expect
      .element(getByPlaceholder(/zhulong\/alarms/i))
      .toBeInTheDocument()

    await userEvent.click(getByRole('switch', { name: '设备存活上报' }))
    await expect.element(getByText(/独立状态 Topic/)).toBeInTheDocument()
    await expect
      .element(getByRole('switch', { name: /断线遗嘱通知/ }))
      .toBeChecked()
  })

  it('shows the inline image budget only for MQTT base64 mode', async () => {
    const queryClient = new QueryClient()
    const { getByText, getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertDestinationDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
        />
      </QueryClientProvider>
    )

    // Webhook (multipart) has no inline budget.
    await expect.element(getByText(/内联图片上限/)).not.toBeInTheDocument()

    // Switching to MQTT flips multipart to base64 and reveals the budget field.
    await userEvent.click(getByText('MQTT 消息总线', { exact: true }))
    await expect.element(getByText(/内联图片上限/)).toBeInTheDocument()
    await expect
      .element(getByRole('spinbutton', { name: /内联图片上限/ }))
      .toHaveValue(500)
    await expect.element(getByText(/AWS IoT 128 KB/)).toBeInTheDocument()

    await userEvent.click(getByText('HTTP Webhook', { exact: true }))
    await expect.element(getByText(/内联图片上限/)).not.toBeInTheDocument()
  })

  it('validates form before running inline connection probe test and succeeds when valid', async () => {
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

    const testBtn = getByRole('button', { name: /测试连通性/i })
    await userEvent.click(testBtn)

    // Empty name & url must block test request and show field validation errors
    await expect.element(getByText('请输入通道名称')).toBeInTheDocument()
    await expect.element(getByText('请输入 Webhook URL')).toBeInTheDocument()
    expect(alertPushApi.test).not.toHaveBeenCalled()

    await userEvent.type(
      getByRole('textbox', { name: /通道名称/i }),
      'Probe Webhook'
    )
    await userEvent.type(
      getByPlaceholder(/https:\/\/api.example.com\/alerts\/webhook/i),
      'https://api.example.com/hook'
    )
    await userEvent.click(testBtn)

    await vi.waitFor(() => {
      expect(alertPushApi.test).toHaveBeenCalledTimes(1)
    })
    await expect.element(getByText(/探测成功 \(24ms\)/i)).toBeInTheDocument()
  })

  it('binds 422 VALIDATION_FAILED field details and shows specific reason on inline test failure', async () => {
    vi.mocked(alertPushApi.test).mockRejectedValueOnce(
      makeAxiosError(422, {
        code: 'VALIDATION_FAILED',
        message: '请求参数校验失败',
        details: [
          {
            field: 'config.url',
            code: 'ALERT_URL_BLOCKED',
            message: '目标地址命中 SSRF 防护策略被拦截',
          },
        ],
      })
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
      'Loopback Webhook'
    )
    await userEvent.type(
      getByPlaceholder(/https:\/\/api.example.com\/alerts\/webhook/i),
      'http://127.0.0.1:8000/hook'
    )
    await userEvent.click(getByRole('button', { name: /测试连通性/i }))

    // Specific field error is rendered on the url input AND surfaced in the test failure pill
    await expect
      .element(getByText('目标地址命中 SSRF 防护策略被拦截', { exact: true }))
      .toBeInTheDocument()
    await expect
      .element(
        getByText('探测失败: 目标地址命中 SSRF 防护策略被拦截', { exact: true })
      )
      .toBeInTheDocument()
  })

  it('omits hidden heartbeat fields when heartbeatEnabled is turned off before saving', async () => {
    const crossOriginHbDest: AlertDestination = {
      ...mockDestination,
      id: 'dest-hb-off',
      headerNames: [],
      config: {
        url: 'https://api.example.com/alerts/webhook',
        heartbeatUrl: 'https://other.example.org/heartbeat',
        authType: 'none',
        timeoutSeconds: 5,
        heartbeatIntervalSeconds: 45,
      },
    }

    const queryClient = new QueryClient()
    const { getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertDestinationDialog
          open={true}
          destination={crossOriginHbDest}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
        />
      </QueryClientProvider>
    )

    // Turn off Device Liveness Reporting
    await userEvent.click(getByRole('switch', { name: '设备存活上报' }))
    await userEvent.click(getByRole('button', { name: /保存目标/i }))

    await vi.waitFor(() => {
      expect(alertPushApi.update).toHaveBeenCalledTimes(1)
    })
    const [, submittedPayload] = vi.mocked(alertPushApi.update).mock.calls[0]
    expect(submittedPayload.heartbeatEnabled).toBe(false)
    expect(submittedPayload.config).not.toHaveProperty('heartbeatUrl')
    expect(submittedPayload.config).not.toHaveProperty(
      'heartbeatIntervalSeconds'
    )
  })

  it('updates retained secret placeholder when switching Webhook authType in edit mode', async () => {
    const bearerDest: AlertDestination = {
      ...mockDestination,
      id: 'dest-bearer-edit',
      headerNames: [],
      secretSet: true,
      config: {
        url: 'https://api.example.com/alerts/webhook',
        authType: 'bearer',
        timeoutSeconds: 5,
        heartbeatIntervalSeconds: 60,
      },
    }

    const queryClient = new QueryClient()
    const { getByRole, getByPlaceholder } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertDestinationDialog
          open={true}
          destination={bearerDest}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
        />
      </QueryClientProvider>
    )

    // Original bearer auth shows retained secret placeholder
    await expect
      .element(getByPlaceholder('凭据已保存，留空表示保持不变'))
      .toBeInTheDocument()

    // Switch authType to Basic -> placeholders must NOT claim secret is retained
    const authTrigger = getByRole('combobox', { name: /鉴权方式/i })
    await userEvent.click(authTrigger)
    await userEvent.click(getByRole('option', { name: 'HTTP Basic Auth' }))

    await expect.element(getByPlaceholder('用户名')).toBeInTheDocument()
    await expect.element(getByPlaceholder('密码')).toBeInTheDocument()
  })

  it('supports explicitly clearing saved MQTT password and Webhook signingSecret', async () => {
    const mqttWithPassword: AlertDestination = {
      id: 'dest-mqtt-pass',
      name: 'Auth MQTT',
      channelType: 'mqtt',
      enabled: true,
      eventsEnabled: true,
      heartbeatEnabled: false,
      imageMode: 'base64',
      config: {
        broker: 'tcp://10.0.0.10:1883',
        topic: 'zhulong/alarms',
        username: 'edge',
        qos: 0,
        keepAliveSeconds: 30,
        inlineMaxKb: 500,
      },
      filter: { eventTypes: [], algorithmIds: [], cameraIds: [] },
      secretSet: true,
      signingSecretSet: false,
      status: {
        consecutiveFailures: 0,
        totalDispatched: 0,
        dropped: 0,
        queueLength: 0,
        queueCapacity: 16,
        state: 'idle',
      },
    }

    const queryClient = new QueryClient()
    const { getByRole, unmount } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertDestinationDialog
          open={true}
          destination={mqttWithPassword}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
        />
      </QueryClientProvider>
    )

    await userEvent.click(getByRole('checkbox', { name: '清除已保存密码' }))
    await userEvent.click(getByRole('button', { name: /保存目标/i }))

    await vi.waitFor(() => {
      expect(alertPushApi.update).toHaveBeenCalledTimes(1)
    })
    expect(alertPushApi.update).toHaveBeenLastCalledWith(
      'dest-mqtt-pass',
      expect.objectContaining({
        secret: expect.objectContaining({ password: '' }),
      })
    )
    await unmount()

    // Now test Webhook signingSecret clear
    const webhookWithSigning: AlertDestination = {
      ...mockDestination,
      id: 'dest-wh-sign',
      headerNames: [],
      signingSecretSet: true,
    }
    const { getByRole: getByRoleWh } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertDestinationDialog
          open={true}
          destination={webhookWithSigning}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
        />
      </QueryClientProvider>
    )

    await userEvent.click(
      getByRoleWh('checkbox', { name: '清除已保存签名密钥' })
    )
    await userEvent.click(getByRoleWh('button', { name: /保存目标/i }))

    await vi.waitFor(() => {
      expect(alertPushApi.update).toHaveBeenCalledTimes(2)
    })
    expect(alertPushApi.update).toHaveBeenLastCalledWith(
      'dest-wh-sign',
      expect.objectContaining({
        secret: expect.objectContaining({ signingSecret: '' }),
      })
    )
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
    const onOpenChange = vi.fn()
    const { getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertDestinationDialog
          open={true}
          destination={mockDestination}
          onOpenChange={onOpenChange}
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

    // 保存成功后通知父级刷新并关闭抽屉
    await vi.waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(2))
    expect(onOpenChange).toHaveBeenLastCalledWith(false)
  })

  it('clears retained placeholder immediately when renaming an existing custom header key', async () => {
    const queryClient = new QueryClient()
    const { getByPlaceholder } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertDestinationDialog
          open={true}
          destination={mockDestination}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
        />
      </QueryClientProvider>
    )

    const keyInput = getByPlaceholder('X-Custom-Header')
    await expect.element(keyInput).toHaveValue('X-Custom-Token')
    await expect
      .element(getByPlaceholder('凭据已保存，留空表示保持不变'))
      .toBeInTheDocument()

    await userEvent.clear(keyInput)
    await userEvent.type(keyInput, 'X-Renamed-Header')

    await expect.element(getByPlaceholder('Header 对应值')).toBeInTheDocument()
  })

  it('maps 409 conflict banner and 422 VALIDATION_FAILED field details including collapsed advanced fields', async () => {
    vi.mocked(alertPushApi.create)
      .mockRejectedValueOnce(
        makeAxiosError(409, {
          code: 'ALERT_DESTINATION_NAME_CONFLICT',
          message: '告警推送目标名称或 ClientID 已存在',
        })
      )
      .mockRejectedValueOnce(
        makeAxiosError(422, {
          code: 'VALIDATION_FAILED',
          message: 'Validation failed',
          details: [
            {
              field: 'secret.signingSecret',
              code: 'INVALID',
              message: '签名密钥长度不足',
            },
          ],
        })
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
      'Conflict Webhook'
    )
    await userEvent.type(
      getByPlaceholder(/https:\/\/api.example.com\/alerts\/webhook/i),
      'https://api.example.com/hook'
    )

    // 1. 409 conflict banner
    await userEvent.click(getByRole('button', { name: /保存目标/i }))
    await expect
      .element(getByText('告警推送目标名称或 ClientID 已存在'))
      .toBeInTheDocument()

    // 2. 422 field detail inside collapsed Webhook advanced section auto-expands and displays message
    await userEvent.click(getByRole('button', { name: /保存目标/i }))
    await expect.element(getByText('签名密钥长度不足')).toBeInTheDocument()
  })

  it('displays orphan filter IDs and allows removing them via keyboard-accessible buttons', async () => {
    const destWithOrphan: AlertDestination = {
      ...mockDestination,
      id: 'dest-orphan',
      filter: {
        eventTypes: [],
        algorithmIds: ['algo-1', 'algo-removed-99'],
        cameraIds: [],
      },
    }

    const queryClient = new QueryClient()
    const { getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertDestinationDialog
          open={true}
          destination={destWithOrphan}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
        />
      </QueryClientProvider>
    )

    const orphanBtn = getByRole('button', {
      name: 'algo-removed-99 (已移除)',
    })
    await expect.element(orphanBtn).toBeInTheDocument()
    await userEvent.click(orphanBtn)
    await expect.element(orphanBtn).not.toBeInTheDocument()
  })

  it('keeps the drawer footer pinned when custom headers grow', async () => {
    const queryClient = new QueryClient()
    const { getByRole } = await render(
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

    const { sheet, scroll, footer } = getDrawerParts()
    await settledSheet(sheet)
    const footerTop = footer.getBoundingClientRect().top

    for (let i = 0; i < 3; i += 1) {
      await userEvent.click(getByRole('button', { name: '添加标头' }))
    }

    // 标头行增多：新增行进入滚动区，横向布局不撑破，底栏仍贴在抽屉底边
    expect(getByRole('button', { name: '移除标头' }).all()).toHaveLength(4)
    expect(scroll.scrollWidth).toBeLessThanOrEqual(scroll.clientWidth + 1)
    expect(Math.round(footer.getBoundingClientRect().top)).toBe(
      Math.round(footerTop)
    )
    expect(Math.round(footer.getBoundingClientRect().bottom)).toBe(
      Math.round(sheet.getBoundingClientRect().bottom)
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

  it('renders capability badges, dispatched count, and em-dash queue cell for heartbeat-only rows in table', async () => {
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
      status: {
        ...mockDestination.status,
        totalDispatched: 0,
        lastSuccessAt: undefined,
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
    await expect.element(getByText('累计 10 条')).toBeInTheDocument()
    await expect.element(getByText(/^成功 /)).toBeInTheDocument()
  })

  it('renders connecting status badge and dropped overflow indicator in table', async () => {
    const connectingMqtt: AlertDestination = {
      id: 'dest-mqtt-conn',
      name: 'Edge Broker',
      channelType: 'mqtt',
      enabled: true,
      eventsEnabled: true,
      heartbeatEnabled: false,
      imageMode: 'base64',
      config: {
        broker: 'tcp://10.0.0.10:1883',
        topic: 'zhulong/events',
      },
      filter: {
        eventTypes: [],
        algorithmIds: [],
        cameraIds: [],
      },
      secretSet: false,
      status: {
        consecutiveFailures: 0,
        totalDispatched: 0,
        dropped: 7,
        queueLength: 16,
        queueCapacity: 16,
        state: 'connecting',
      },
    }

    const { getByText } = await render(
      <AlertDestinationsTable
        destinations={[connectingMqtt]}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
        onToggle={vi.fn()}
        onTest={vi.fn()}
      />
    )

    await expect.element(getByText('连接中')).toBeInTheDocument()
    await expect.element(getByText('16 / 16')).toBeInTheDocument()
    await expect.element(getByText('丢弃 7')).toBeInTheDocument()
  })

  it('auto-expands MQTT advanced section in edit mode when custom MQTT options are set', async () => {
    const customMqttDest: AlertDestination = {
      id: '01J8ZMQTTEDIT000000000001',
      name: 'TLS MQTT Broker',
      channelType: 'mqtt',
      enabled: true,
      eventsEnabled: true,
      heartbeatEnabled: false,
      imageMode: 'base64',
      config: {
        broker: 'wss://broker.example.com:8084/mqtt',
        topic: 'zhulong/events',
        clientId: 'edge-custom-id',
        qos: 1,
        keepAliveSeconds: 60,
        tlsSkipVerify: true,
        inlineMaxKb: 500,
      },
      filter: {
        eventTypes: [],
        algorithmIds: [],
        cameraIds: [],
      },
      secretSet: false,
      status: {
        consecutiveFailures: 0,
        totalDispatched: 5,
        dropped: 0,
        queueLength: 0,
        queueCapacity: 16,
        state: 'running',
      },
    }

    const queryClient = new QueryClient()
    const { getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertDestinationDialog
          open={true}
          destination={customMqttDest}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
        />
      </QueryClientProvider>
    )

    // Client ID input is inside the collapsible MQTT advanced section; it must be visible without clicking toggle.
    await expect
      .element(getByRole('textbox', { name: /Client ID/i }))
      .toHaveValue('edge-custom-id')
  })

  it('auto-expands Webhook advanced section and submits tlsSkipVerify in edit mode', async () => {
    const selfSignedWebhook: AlertDestination = {
      ...mockDestination,
      id: 'dest-webhook-tls',
      name: 'Self-Signed Webhook',
      headerNames: [],
      config: {
        url: 'https://10.0.0.20:8443/alerts',
        authType: 'none',
        timeoutSeconds: 5,
        tlsSkipVerify: true,
        heartbeatIntervalSeconds: 60,
      },
    }

    const queryClient = new QueryClient()
    const { getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <AlertDestinationDialog
          open={true}
          destination={selfSignedWebhook}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
        />
      </QueryClientProvider>
    )

    const tlsSwitch = getByRole('switch', { name: '跳过 TLS 证书校验' })
    await expect.element(tlsSwitch).toBeChecked()

    await userEvent.click(getByRole('button', { name: /保存目标/i }))
    await vi.waitFor(() => {
      expect(alertPushApi.update).toHaveBeenCalledTimes(1)
    })
    expect(alertPushApi.update).toHaveBeenLastCalledWith(
      'dest-webhook-tls',
      expect.objectContaining({
        config: expect.objectContaining({ tlsSkipVerify: true }),
      })
    )
  })
})
