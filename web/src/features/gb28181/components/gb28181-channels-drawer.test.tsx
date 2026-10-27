import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { gb28181Api } from '../api/gb28181-api'
import { type Gb28181Channel, type Gb28181Device } from '../data/schema'
import { Gb28181ChannelsDrawer } from './gb28181-channels-drawer'

const { syncMutateAsync, addCameraMutateAsync } = vi.hoisted(() => ({
  syncMutateAsync: vi.fn(),
  addCameraMutateAsync: vi.fn(),
}))

vi.mock('../hooks/use-gb28181', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../hooks/use-gb28181')>()
  return {
    ...actual,
    useSyncGb28181Catalog: () => ({
      mutateAsync: syncMutateAsync,
      isPending: false,
    }),
    useAddGb28181ChannelCamera: () => ({
      mutateAsync: addCameraMutateAsync,
      isPending: false,
    }),
  }
})

const onlineDevice: Gb28181Device = {
  deviceId: '34020000001320000001',
  name: '东门球机',
  manufacturer: null,
  model: null,
  firmware: null,
  transport: 'udp',
  remoteAddr: '192.168.1.64:5060',
  expiresSeconds: 3600,
  state: 'online',
  channelCount: 0,
  lastKeepaliveAt: '2026-10-25T08:00:00Z',
  registeredAt: '2026-10-25T07:00:00Z',
  createdAt: '2026-10-25T07:00:00Z',
  updatedAt: '2026-10-25T08:00:00Z',
}

const offlineDevice: Gb28181Device = {
  ...onlineDevice,
  deviceId: '34020000001320000002',
  state: 'offline',
}

const sampleChannels: Gb28181Channel[] = [
  {
    deviceId: '34020000001320000001',
    channelId: '34020000001310000001',
    name: 'Main Entrance',
    manufacturer: 'Hikvision',
    model: 'DS-2CD',
    owner: null,
    civilCode: null,
    address: null,
    parental: 0,
    parentId: null,
    safetyWay: null,
    registerWay: null,
    ptzType: 1,
    resolutionWidth: 1920,
    resolutionHeight: 1080,
    cameraId: null,
    createdAt: '2026-10-25T08:00:00Z',
    updatedAt: '2026-10-25T08:00:00Z',
  },
  {
    deviceId: '34020000001320000001',
    channelId: '34020000001310000002',
    name: 'Parking Lot',
    manufacturer: 'Hikvision',
    model: 'DS-2CD',
    owner: null,
    civilCode: null,
    address: null,
    parental: 0,
    parentId: null,
    safetyWay: null,
    registerWay: null,
    ptzType: 2,
    resolutionWidth: 2560,
    resolutionHeight: 1440,
    cameraId: 'cam_existing_123',
    createdAt: '2026-10-25T08:00:00Z',
    updatedAt: '2026-10-25T08:00:00Z',
  },
]

function renderDrawer(
  props: Partial<React.ComponentProps<typeof Gb28181ChannelsDrawer>> = {}
) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })

  return render(
    <QueryClientProvider client={queryClient}>
      <Gb28181ChannelsDrawer
        device={onlineDevice}
        open={true}
        onOpenChange={vi.fn()}
        {...props}
      />
    </QueryClientProvider>
  )
}

describe('Gb28181ChannelsDrawer', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders channels with resolution and camera status', async () => {
    vi.spyOn(gb28181Api, 'listChannels').mockResolvedValueOnce(sampleChannels)

    const { getByText } = await renderDrawer()

    await expect.element(getByText('通道列表')).toBeInTheDocument()
    await expect.element(getByText('34020000001310000001')).toBeInTheDocument()
    await expect.element(getByText('Main Entrance')).toBeInTheDocument()
    await expect.element(getByText('1920x1080')).toBeInTheDocument()
    await expect.element(getByText('添加为摄像机')).toBeInTheDocument()

    await expect.element(getByText('34020000001310000002')).toBeInTheDocument()
    await expect.element(getByText('Parking Lot')).toBeInTheDocument()
    await expect.element(getByText('2560x1440')).toBeInTheDocument()
    await expect.element(getByText('已添加')).toBeInTheDocument()
  })

  it('disables sync catalog button when device is offline', async () => {
    vi.spyOn(gb28181Api, 'listChannels').mockResolvedValueOnce([])

    const { getByRole, getByText } = await renderDrawer({
      device: offlineDevice,
    })

    await expect
      .element(getByRole('button', { name: '同步目录' }))
      .toBeDisabled()
    await expect
      .element(getByText('设备当前处于离线状态，无法下发目录查询指令。'))
      .toBeInTheDocument()
  })

  it('renders the localized failure title when the channel list cannot be loaded', async () => {
    vi.spyOn(gb28181Api, 'listChannels').mockRejectedValueOnce(
      new Error('boom')
    )

    const { getByText } = await renderDrawer()

    // 标题必须来自 `channels.loadFailed`：此前用的是本命名空间里不存在的
    // `common.error`，三语下都会渲染成硬编码英文。
    await expect.element(getByText('读取通道列表失败')).toBeInTheDocument()
  })

  it('triggers sync catalog on button click', async () => {
    vi.spyOn(gb28181Api, 'listChannels').mockResolvedValueOnce([])
    syncMutateAsync.mockResolvedValueOnce({
      deviceId: onlineDevice.deviceId,
      total: 2,
    })

    const { getByRole } = await renderDrawer()

    await userEvent.click(getByRole('button', { name: '同步目录' }))

    await vi.waitFor(() => {
      expect(syncMutateAsync).toHaveBeenCalledWith(onlineDevice.deviceId)
    })
  })

  it('triggers add channel as camera on button click', async () => {
    vi.spyOn(gb28181Api, 'listChannels').mockResolvedValueOnce([
      sampleChannels[0],
    ])
    addCameraMutateAsync.mockResolvedValueOnce({ id: 'cam_new' })

    const { getByRole } = await renderDrawer()

    await userEvent.click(getByRole('button', { name: '添加为摄像机' }))

    await vi.waitFor(() => {
      expect(addCameraMutateAsync).toHaveBeenCalledWith({
        deviceId: onlineDevice.deviceId,
        channelId: sampleChannels[0].channelId,
      })
    })
  })
})
