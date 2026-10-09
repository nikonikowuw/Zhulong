import { describe, it, expect, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { type LiveCameraItem, type LiveCellState } from '../types'
import { LivePlayerCell } from './live-player-cell'

describe('LivePlayerCell 交互测试', () => {
  const dummyCamera: LiveCameraItem = {
    id: 'cam-01',
    name: '东大门测试枪机',
    ip: '192.168.1.101',
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
})
