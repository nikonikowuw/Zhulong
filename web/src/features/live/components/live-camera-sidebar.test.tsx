import { describe, it, expect, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { type LiveCameraItem } from '../types'
import { LiveCameraSidebar } from './live-camera-sidebar'

function camera(overrides: Partial<LiveCameraItem> = {}): LiveCameraItem {
  return {
    id: 'cam-01',
    name: '东大门枪机',
    host: '192.168.1.101',
    status: 'online',
    hasSubStream: false,
    codec: 'h264',
    resolution: '1920x1080',
    ...overrides,
  }
}

function renderSidebar(cameras: LiveCameraItem[]) {
  return render(
    <LiveCameraSidebar
      cameras={cameras}
      activeCellId={0}
      onAssignCamera={vi.fn()}
      isCollapsed={false}
      onToggleCollapse={vi.fn()}
    />
  )
}

describe('LiveCameraSidebar', () => {
  it('renders resolution and codec instead of the target host', async () => {
    const { getByText, container } = await renderSidebar([camera()])

    expect(getByText('东大门枪机')).toBeDefined()
    expect(getByText('1920x1080')).toBeDefined()
    expect(getByText('H264')).toBeDefined()
    // 主机会移到悬浮提示中，不再占用条目正文
    expect(container.textContent).not.toContain('192.168.1.101')
  })

  it('omits the resolution slot when the backend reports no dimensions', async () => {
    const { container } = await renderSidebar([
      camera({ id: 'cam-02', resolution: undefined }),
    ])

    expect(container.textContent).not.toContain('undefined')
    expect(container.textContent).toContain('H264')
  })

  it('filters by host so operators can still locate same-named devices', async () => {
    const { getByRole, getByText } = await renderSidebar([
      camera({ id: 'cam-01', name: '摄像头1', host: '10.0.0.11' }),
      camera({ id: 'cam-02', name: '摄像头2', host: '10.0.0.12' }),
      camera({ id: 'cam-03', name: '摄像头3', host: undefined }),
    ])

    const input = getByRole('textbox')
    await expect.element(input).toBeVisible()
    await input.fill('10.0.0.12')

    await expect.element(getByText('摄像头2')).toBeVisible()
    await expect.element(getByText('摄像头1')).not.toBeInTheDocument()
    await expect.element(getByText('摄像头3')).not.toBeInTheDocument()
  })

  it('never fabricates a host when the backend provides none', async () => {
    const { container } = await renderSidebar([
      camera({ host: undefined, resolution: undefined }),
    ])

    expect(container.textContent).not.toContain('127.0.0.1')
  })
})
