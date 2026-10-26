import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { Button } from '@/components/ui/button'
import { type Camera } from '../data/schema'
import { CamerasActionDialog } from './cameras-action-dialog'
import { CamerasProvider, useCamerasContext } from './cameras-provider'

const { updateCamera } = vi.hoisted(() => ({ updateCamera: vi.fn() }))

vi.mock('../hooks/use-cameras', () => ({
  useCreateCamera: () => ({
    mutateAsync: vi.fn(),
    isPending: false,
  }),
  useUpdateCamera: () => ({
    mutateAsync: updateCamera,
    isPending: false,
  }),
}))

const gbCamera: Camera = {
  id: '01J9GB28181CAMERA0000000000',
  name: 'East gate channel',
  enabled: true,
  revision: 4,
  health: 'unknown',
  session: 'idle',
  degraded: false,
  stale: false,
  reason: '',
  streams: [
    {
      id: 1,
      role: 'main',
      protocol: 'gb28181',
      rtspUrl: 'gb28181://34020000001320000001/34020000001310000001',
      transport: 'udp',
      codec: '',
      width: 0,
      height: 0,
      fpsString: '',
    },
  ],
  createdAt: '2026-10-25T07:00:00Z',
  updatedAt: '2026-10-25T07:00:00Z',
}

function TestComponent() {
  const { setOpen, setCurrentRow } = useCamerasContext()
  return (
    <div>
      <Button onClick={() => setOpen('create')}>Open Create</Button>
      <Button
        onClick={() => {
          setCurrentRow(gbCamera)
          setOpen('edit')
        }}
      >
        Open GB Edit
      </Button>
      <CamerasActionDialog />
    </div>
  )
}

describe('CamerasActionDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders create dialog with name and rtsp fields when opened', async () => {
    const queryClient = new QueryClient()
    const { getByRole, getByPlaceholder } = await render(
      <QueryClientProvider client={queryClient}>
        <CamerasProvider>
          <TestComponent />
        </CamerasProvider>
      </QueryClientProvider>
    )

    await userEvent.click(getByRole('button', { name: /Open Create/i }))
    await expect
      .element(getByRole('heading', { name: /新增摄像机资产/i }))
      .toBeInTheDocument()
    await expect
      .element(getByPlaceholder(/例如：园区主大门 4K 枪机/i))
      .toBeInTheDocument()
    await expect
      .element(getByRole('button', { name: /确认添加/i }))
      .toBeInTheDocument()
  })

  it('updates a GB28181 camera without submitting its stream as RTSP', async () => {
    const queryClient = new QueryClient()
    const { getByRole, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <CamerasProvider>
          <TestComponent />
        </CamerasProvider>
      </QueryClientProvider>
    )

    await userEvent.click(getByRole('button', { name: 'Open GB Edit' }))
    await expect
      .element(getByRole('heading', { name: '编辑摄像机配置' }))
      .toBeInTheDocument()
    await expect
      .element(
        getByText('此 GB28181 码流由设备目录管理；这里仅可修改名称和启用状态。')
      )
      .toBeInTheDocument()
    await expect
      .element(getByText('主流 RTSP 地址', { exact: true }))
      .not.toBeInTheDocument()

    await userEvent.click(getByRole('button', { name: '保存更改' }))
    await vi.waitFor(() => expect(updateCamera).toHaveBeenCalledTimes(1))

    expect(updateCamera).toHaveBeenCalledWith({
      id: gbCamera.id,
      data: {
        revision: gbCamera.revision,
        name: gbCamera.name,
        enabled: gbCamera.enabled,
      },
    })
  })
})
