import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { type Gb28181RuntimeInfo, type Gb28181Snapshot } from '../data/schema'
import { Gb28181RuntimePanel } from './gb28181-runtime-panel'

const mockRuntime: Gb28181RuntimeInfo = {
  startupConfig: {
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
  },
  state: 'running',
  detail: '',
  advertisedIp: '192.168.1.10',
  restartRequired: false,
  mediaCapacity: 50,
}

const mockSnapshot: Gb28181Snapshot = {
  state: 'running',
  restartRequired: false,
  devices: {
    total: 10,
    online: 6,
  },
  sessions: {
    active: 3,
  },
  portPool: {
    pairsTotal: 50,
    pairsUsed: 6,
    exhaustedHits: 0,
  },
  generatedAt: '2026-10-25T08:00:00Z',
}

describe('Gb28181RuntimePanel', () => {
  it('renders runtime metrics and active status badge', async () => {
    const { getByText } = await render(
      <Gb28181RuntimePanel
        runtimeInfo={mockRuntime}
        snapshot={mockSnapshot}
        isLoading={false}
        isError={false}
        isFetching={false}
        onRetry={vi.fn()}
      />
    )

    await expect.element(getByText('运行态')).toBeInTheDocument()
    await expect.element(getByText('运行中')).toBeInTheDocument()
    await expect.element(getByText('6 / 10')).toBeInTheDocument()
    await expect.element(getByText('3')).toBeInTheDocument()
    await expect.element(getByText('6 / 50')).toBeInTheDocument()
    await expect.element(getByText('192.168.1.10')).toBeInTheDocument()
    await expect.element(getByText(/50 路/)).toBeInTheDocument()
  })

  it('renders error alert with message and triggers retry', async () => {
    const onRetry = vi.fn()
    const { getByRole, getByText } = await render(
      <Gb28181RuntimePanel
        isLoading={false}
        isError={true}
        errorMessage='连接服务失败，请检查服务状态'
        isFetching={false}
        onRetry={onRetry}
      />
    )

    await expect.element(getByText('读取运行态失败')).toBeInTheDocument()
    await expect
      .element(getByText('连接服务失败，请检查服务状态'))
      .toBeInTheDocument()

    const retryBtn = getByRole('button', { name: '重试' })
    await userEvent.click(retryBtn)
    expect(onRetry).toHaveBeenCalledTimes(1)
  })

  it('renders diagnostic detail banner and error badge on runtime error state', async () => {
    const errorRuntime: Gb28181RuntimeInfo = {
      ...mockRuntime,
      state: 'error',
      detail: 'ingress_start_failed',
    }

    const { getByText } = await render(
      <Gb28181RuntimePanel
        runtimeInfo={errorRuntime}
        snapshot={mockSnapshot}
        isLoading={false}
        isError={false}
        isFetching={false}
        onRetry={vi.fn()}
      />
    )

    await expect.element(getByText('故障')).toBeInTheDocument()
    await expect.element(getByText('运行诊断')).toBeInTheDocument()
    await expect.element(getByText('SIP 监听启动失败')).toBeInTheDocument()
  })

  it('shows skeleton cards during initial loading', async () => {
    const { container } = await render(
      <Gb28181RuntimePanel
        isLoading={true}
        isError={false}
        isFetching={false}
        onRetry={vi.fn()}
      />
    )

    expect(container.querySelectorAll('.animate-pulse').length).toBeGreaterThan(
      0
    )
  })
})
