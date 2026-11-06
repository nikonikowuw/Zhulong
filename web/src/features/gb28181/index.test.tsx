import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { gb28181Api } from './api/gb28181-api'
import {
  type Gb28181Config,
  type Gb28181ConfigResponse,
  type Gb28181RuntimeSnapshotStatus,
} from './data/schema'
import { SettingsGb28181 } from './index'

const { navigate } = vi.hoisted(() => ({ navigate: vi.fn() }))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    getRouteApi: (id: string) => {
      if (id === '/_authenticated/settings/gb28181') {
        return {
          useSearch: () => ({}),
          useNavigate: () => navigate,
        }
      }
      return actual.getRouteApi(id as never)
    },
  }
})

vi.mock('./components/gb28181-devices-table', () => ({
  Gb28181DevicesTable: () => <div data-testid='devices-table' />,
}))

const config: Gb28181Config = {
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
}

const configResponse: Gb28181ConfigResponse = {
  config,
  runtime: {
    startupConfig: config,
    state: 'running',
    detail: '',
    advertisedIp: '192.168.1.10',
    restartRequired: false,
    mediaCapacity: 50,
  },
}

const runtimeSnapshot: Gb28181RuntimeSnapshotStatus = {
  state: 'running',
  restartRequired: false,
  devices: { total: 0, online: 0 },
  sessions: { active: 0 },
  portPool: { pairsTotal: 50, pairsUsed: 0, exhaustedHits: 0 },
  ingress: {
    oversizedMessages: 0,
    rejectedConns: 0,
    inboundLimited: 0,
    outboundLimited: 0,
    activeConns: 0,
  },
  generatedAt: '2026-10-25T08:00:00Z',
}

function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
}

describe('SettingsGb28181', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    navigate.mockReset()
    vi.spyOn(gb28181Api, 'getConfig').mockResolvedValue(configResponse)
    vi.spyOn(gb28181Api, 'getRuntime').mockResolvedValue(runtimeSnapshot)
    vi.spyOn(gb28181Api, 'listDevices').mockResolvedValue([])
  })

  it('retains unsaved configuration values when switching tabs', async () => {
    const queryClient = createQueryClient()
    const { getByLabelText, getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <SettingsGb28181 />
      </QueryClientProvider>
    )

    await userEvent.click(getByRole('tab', { name: /目标配置/ }))
    const sipIdInput = getByLabelText('SIP 服务端 ID')
    await expect.element(sipIdInput).toBeInTheDocument()
    await userEvent.clear(sipIdInput)
    await userEvent.type(sipIdInput, '34020000002000000002')

    await userEvent.click(getByRole('tab', { name: /设备白名单/ }))
    await userEvent.click(getByRole('tab', { name: /目标配置/ }))

    await expect
      .element(getByLabelText('SIP 服务端 ID'))
      .toHaveValue('34020000002000000002')
  })
})
