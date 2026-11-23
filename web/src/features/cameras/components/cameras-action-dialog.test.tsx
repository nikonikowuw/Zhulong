import { AxiosError, AxiosHeaders } from 'axios'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { Button } from '@/components/ui/button'
import { camerasApi } from '../api/cameras-api'
import { type Camera } from '../data/schema'
import { CamerasActionDialog } from './cameras-action-dialog'
import { CamerasProvider, useCamerasContext } from './cameras-provider'

const { updateCamera } = vi.hoisted(() => ({ updateCamera: vi.fn() }))

vi.mock('../hooks/use-cameras', () => ({
  CAMERAS_QUERY_KEY: ['cameras'],
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

// 取当前抽屉的三段结构（内容容器 / 滚动区 / 底栏），缺失即断言失败
function getDrawerParts() {
  const last = (selector: string) => {
    const nodes = document.querySelectorAll(selector)
    return nodes[nodes.length - 1]
  }
  const sheet = last('[data-slot="sheet-content"]')
  const scroll = last('[data-testid="cameras-action-scroll"]')
  const footer = last('[data-slot="sheet-footer"]')
  if (
    !(sheet instanceof HTMLElement) ||
    !(scroll instanceof HTMLElement) ||
    !(footer instanceof HTMLElement)
  ) {
    throw new Error('camera drawer structure is incomplete')
  }
  return { sheet, scroll, footer }
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

    // 抽屉形态：右侧 Sheet 承载表单，居中 Dialog 已不再出现
    expect(document.querySelector('[data-slot="dialog-content"]')).toBeNull()
    const { sheet, scroll, footer } = getDrawerParts()
    const viewportRight = () =>
      document.documentElement.getBoundingClientRect().right
    // 入场动画落位后再量几何，避免量到 slide-in 中间态
    await vi.waitFor(() =>
      expect(
        Math.abs(sheet.getBoundingClientRect().right - viewportRight())
      ).toBeLessThanOrEqual(1)
    )

    // 抽屉自带的关闭按钮（sr-only "Close"）常驻右上角，不依赖业务表单实现
    await expect
      .element(getByRole('button', { name: 'Close' }))
      .toBeInTheDocument()

    const sheetRect = sheet.getBoundingClientRect()
    expect(Math.round(sheetRect.top)).toBe(0)
    // 移动端铺满视口：左缘贴 0、右缘贴内联末端（滚动条会带来几像素误差）
    expect(Math.abs(sheetRect.left)).toBeLessThanOrEqual(1)
    expect(Math.abs(sheetRect.right - viewportRight())).toBeLessThanOrEqual(1)
    // 右缘锚定：收窄抽屉后仍然贴住视口内联末端，左锚定会露馅
    const previousMaxWidth = sheet.style.maxWidth
    sheet.style.maxWidth = '240px'
    expect(
      Math.abs(sheet.getBoundingClientRect().right - viewportRight())
    ).toBeLessThanOrEqual(1)
    expect(sheet.getBoundingClientRect().left).toBeGreaterThan(100)
    sheet.style.maxWidth = previousMaxWidth

    // 只有中部滚动区滚动，底栏固定在抽屉底边
    expect(getComputedStyle(scroll).overflowY).toBe('auto')
    expect(Math.round(footer.getBoundingClientRect().bottom)).toBe(
      Math.round(sheetRect.bottom)
    )
    expect(scroll.getBoundingClientRect().bottom).toBeLessThanOrEqual(
      footer.getBoundingClientRect().top + 1
    )
    expect(scroll.scrollWidth).toBeLessThanOrEqual(scroll.clientWidth + 1)

    // 滚动内容时底栏与抽屉外框保持原位
    const footerTop = footer.getBoundingClientRect().top
    scroll.scrollTop = 10_000
    expect(Math.round(footer.getBoundingClientRect().top)).toBe(
      Math.round(footerTop)
    )
    expect(Math.round(sheet.getBoundingClientRect().top)).toBe(
      Math.round(sheetRect.top)
    )
    scroll.scrollTop = 0
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

  it('retains user draft on 409 CAS conflict and refreshes revision for retry', async () => {
    const queryClient = new QueryClient()
    const conflictErr = new AxiosError(
      'Conflict',
      'ERR_BAD_REQUEST',
      undefined,
      undefined,
      {
        status: 409,
        statusText: 'Conflict',
        headers: {},
        config: { headers: new AxiosHeaders() },
        data: { code: 'CONFLICT', message: 'revision conflict' },
      }
    )

    updateCamera.mockRejectedValueOnce(conflictErr).mockResolvedValueOnce({
      ...gbCamera,
      name: 'Updated Gate Name',
      revision: 6,
    })

    const getCameraSpy = vi
      .spyOn(camerasApi, 'getCamera')
      .mockResolvedValueOnce({
        ...gbCamera,
        revision: 5,
      })

    const { getByRole, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <CamerasProvider>
          <TestComponent />
        </CamerasProvider>
      </QueryClientProvider>
    )

    await userEvent.click(getByRole('button', { name: 'Open GB Edit' }))
    const nameInput = getByRole('textbox', { name: '摄像机名称' })
    await userEvent.clear(nameInput)
    await userEvent.type(nameInput, 'Updated Gate Name')

    await userEvent.click(getByRole('button', { name: '保存更改' }))
    await expect.element(getByText('revision conflict')).toBeInTheDocument()

    // Draft is preserved across conflict
    await expect.element(nameInput).toHaveValue('Updated Gate Name')

    // Refresh latest revision and retry
    await userEvent.click(getByRole('button', { name: '刷新最新配置' }))
    await vi.waitFor(() =>
      expect(getCameraSpy).toHaveBeenCalledWith(gbCamera.id)
    )

    await userEvent.click(getByRole('button', { name: '保存更改' }))
    await vi.waitFor(() => expect(updateCamera).toHaveBeenCalledTimes(2))

    expect(updateCamera).toHaveBeenNthCalledWith(2, {
      id: gbCamera.id,
      data: {
        revision: 5,
        name: 'Updated Gate Name',
        enabled: gbCamera.enabled,
      },
    })
  })

  it('ignores close requests while a submit is pending', async () => {
    let resolveUpdate: (value: unknown) => void = () => {}
    updateCamera.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveUpdate = resolve
        })
    )

    const queryClient = new QueryClient()
    const { getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <CamerasProvider>
          <TestComponent />
        </CamerasProvider>
      </QueryClientProvider>
    )

    await userEvent.click(getByRole('button', { name: 'Open GB Edit' }))
    await userEvent.click(getByRole('button', { name: '保存更改' }))
    await vi.waitFor(() => expect(updateCamera).toHaveBeenCalledTimes(1))

    // 提交在途时 ESC 关闭请求被拦截，抽屉与草稿都保留
    await userEvent.keyboard('{Escape}')
    await expect
      .element(getByRole('heading', { name: '编辑摄像机配置' }))
      .toBeInTheDocument()

    resolveUpdate({ ...gbCamera })
    await vi.waitFor(() =>
      expect(document.querySelector('[data-slot="sheet-content"]')).toBeNull()
    )
  })
})
