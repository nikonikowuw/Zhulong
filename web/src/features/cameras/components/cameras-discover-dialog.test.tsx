import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { Button } from '@/components/ui/button'
import { camerasApi } from '../api/cameras-api'
import { CamerasActionDialog } from './cameras-action-dialog'
import { CamerasDiscoverDialog } from './cameras-discover-dialog'
import { CamerasProvider, useCamerasContext } from './cameras-provider'

function TestDiscoverHarness() {
  const { setOpen } = useCamerasContext()
  return (
    <div>
      <Button
        data-testid='open-discover-btn'
        onClick={() => setOpen('discover')}
      >
        Open Discover
      </Button>
      <CamerasDiscoverDialog />
      <CamerasActionDialog />
    </div>
  )
}

describe('CamerasDiscoverDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('scans and renders discovered cameras then prefills create dialog', async () => {
    const mockDevices = [
      {
        id: 'hik-001',
        ip: '192.168.1.188',
        port: 80,
        xAddr: 'http://192.168.1.188/onvif/device_service',
        name: 'GateEntrance',
        manufacturer: 'Hikvision',
        model: 'DS-2CD2047G2',
        hardware: 'DS-2CD2047G2',
        types: ['dn:NetworkVideoTransmitter'],
        suggestedRtspUrl:
          'rtsp://admin:password@192.168.1.188:554/Streaming/Channels/101',
      },
    ]

    vi.spyOn(camerasApi, 'discoverCameras').mockResolvedValue(mockDevices)

    const queryClient = new QueryClient()
    const screen = await render(
      <QueryClientProvider client={queryClient}>
        <CamerasProvider>
          <TestDiscoverHarness />
        </CamerasProvider>
      </QueryClientProvider>
    )

    // 1. 打开发现弹窗
    const openBtn = screen.getByTestId('open-discover-btn')
    await userEvent.click(openBtn)

    // 2. 验证设备信息渲染
    const nameEl = screen.getByText('GateEntrance')
    await expect.element(nameEl).toBeVisible()

    const ipEl = screen.getByText(/192\.168\.1\.188:80/)
    await expect.element(ipEl).toBeVisible()

    // 3. 点击“以此设备添加”
    const useBtn = screen.getByTestId('use-device-btn')
    await userEvent.click(useBtn)

    // 4. 验证创建表单自动回填
    const nameInput = screen.getByPlaceholder(/例如：园区主大门 4K 枪机/i)
    await expect.element(nameInput).toHaveValue('GateEntrance')

    const rtspInput = screen.getByLabelText('主流 RTSP 地址')
    await expect
      .element(rtspInput)
      .toHaveValue(
        'rtsp://admin:password@192.168.1.188:554/Streaming/Channels/101'
      )
  })
})
