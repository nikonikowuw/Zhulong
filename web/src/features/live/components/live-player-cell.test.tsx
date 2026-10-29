import { beforeEach, describe, it, expect, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { type LiveCameraItem, type LiveCellState } from '../types'
import { LivePlayerCell } from './live-player-cell'

/** 通过 vi.hoisted 暴露替身控制点，避免 mock 工厂访问尚未初始化的顶层绑定。 */
const mocks = vi.hoisted(() => ({
  /** 传输层状态：本文件只关心「连上之后 UI 该显示什么」。 */
  status: 'connected' as string,
  /** 是否支持 WebCodecs：false 代表局域网 HTTP（非安全上下文）。 */
  supportsWebCodecs: false,
}))

vi.mock('../hooks/use-stream-connection', () => ({
  useStreamConnection: () => ({
    status: mocks.status,
    errorMessage: null,
    stats: {},
    retry: vi.fn(),
  }),
}))

vi.mock('../services/webcodecs-decoder', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('../services/webcodecs-decoder')>()
  return {
    ...actual,
    isWebCodecsSupported: () => mocks.supportsWebCodecs,
    getWebCodecsSupportIssue: () =>
      mocks.supportsWebCodecs ? null : 'insecure_context',
  }
})

describe('LivePlayerCell 交互测试', () => {
  beforeEach(() => {
    mocks.status = 'connected'
    mocks.supportsWebCodecs = false
  })

  const dummyCamera: LiveCameraItem = {
    id: 'cam-01',
    name: '东大门测试枪机',
    host: '192.168.1.101',
    status: 'online',
    hasSubStream: true,
    codec: 'h265',
  }

  const emptyCell: LiveCellState = {
    cellId: 2,
    cameraId: null,
    streamType: 'main',
    isMuted: true,
  }

  it('支持拖拽摄像机并成功触发 onDropCamera 回调', async () => {
    const onDropCamera = vi.fn()
    const { container } = await render(
      <LivePlayerCell
        cell={emptyCell}
        isActive={false}
        isMaximized={false}
        onSelect={vi.fn()}
        onClear={vi.fn()}
        onToggleMaximize={vi.fn()}
        onSetMute={vi.fn()}
        onSetStreamType={vi.fn()}
        onDropCamera={onDropCamera}
      />
    )

    const cellEl = container.firstElementChild as HTMLElement
    expect(cellEl).not.toBeNull()

    // 模拟 drop 事件并携带序列化的摄像机对象
    const dropEvent = new Event('drop', { bubbles: true, cancelable: true })
    Object.defineProperty(dropEvent, 'dataTransfer', {
      value: {
        getData: (format: string) =>
          format === 'application/json' ? JSON.stringify(dummyCamera) : '',
      },
    })

    cellEl.dispatchEvent(dropEvent)
    expect(onDropCamera).toHaveBeenCalledWith(dummyCamera)
  })

  it('点击空闲窗口应触发 onSelect 回调', async () => {
    const onSelect = vi.fn()
    const { container } = await render(
      <LivePlayerCell
        cell={emptyCell}
        isActive={false}
        isMaximized={false}
        onSelect={onSelect}
        onClear={vi.fn()}
        onToggleMaximize={vi.fn()}
        onSetMute={vi.fn()}
        onSetStreamType={vi.fn()}
      />
    )

    const cellEl = container.firstElementChild as HTMLElement
    expect(cellEl).not.toBeNull()
    cellEl.click()

    expect(onSelect).toHaveBeenCalled()
  })

  /**
   * 回归保护：非安全上下文下「等待首帧」遮罩绝不能盖住「切换至 HTTPS」引导。
   *
   * 这条路径上解码器根本不存在，首帧永远不会来；只看 status 的遮罩会永久旋转，
   * 用户既看不到原因也点不到出路。
   */
  it('非安全上下文下不展示等待首帧遮罩，而是露出切换 HTTPS 的引导', async () => {
    const cell: LiveCellState = {
      cellId: 1,
      cameraId: 'cam-01',
      cameraName: '东大门测试枪机',
      streamType: 'main',
      isMuted: true,
    }

    const { container } = await render(
      <LivePlayerCell
        cell={cell}
        isActive={false}
        isMaximized={false}
        onSelect={vi.fn()}
        onClear={vi.fn()}
        onToggleMaximize={vi.fn()}
        onSetMute={vi.fn()}
        onSetStreamType={vi.fn()}
      />
    )

    const text = container.textContent ?? ''
    expect(text).not.toContain('等待首帧渲染')
    expect(text).toContain('WebCodecs 需要安全上下文')
    expect(text).toContain('切换至 HTTPS')
  })
})
