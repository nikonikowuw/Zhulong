import { beforeEach, describe, it, expect, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { type LiveCellState } from '../types'
import { LiveGrid } from './live-grid'

const mocks = vi.hoisted(() => ({
  status: 'connected',
  supportsWebCodecs: false,
  streamInfo: null,
}))

vi.mock('../hooks/use-stream-connection', () => ({
  useStreamConnection: () => ({
    status: mocks.status,
    errorMessage: null,
    stats: {},
    retry: vi.fn(),
    streamInfo: mocks.streamInfo,
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

describe('LiveGrid 组件最大化与布局测试', () => {
  beforeEach(() => {
    mocks.status = 'connected'
    mocks.supportsWebCodecs = false
    mocks.streamInfo = null
  })

  const dummyCells: LiveCellState[] = Array.from({ length: 16 }, (_, idx) => ({
    cellId: idx,
    cameraId: idx < 4 ? `cam-0${idx + 1}` : null,
    cameraName: idx < 4 ? `相机 0${idx + 1}` : undefined,
    streamType: 'main',
    isMuted: true,
  }))

  it('四分屏下渲染 4 个单元格，未最大化时不含 hidden 类', async () => {
    const { container } = await render(
      <LiveGrid
        layout={4}
        activeCellId={0}
        maximizedCellId={null}
        cells={dummyCells}
        onSelectCell={vi.fn()}
        onClearCell={vi.fn()}
        onToggleMaximizeCell={vi.fn()}
        onSetCellMute={vi.fn()}
        onSetCellStreamType={vi.fn()}
      />
    )

    const cellWrappers = container.querySelectorAll('.grid > div.h-full')
    expect(cellWrappers.length).toBe(4)

    cellWrappers.forEach((wrapper) => {
      expect(wrapper.classList.contains('hidden')).toBe(false)
      expect(wrapper.classList.contains('absolute')).toBe(false)
    })
  })

  it('最大化单元格时保持 DOM 树节点挂载，最大化项绝对定位且其余项置为 hidden', async () => {
    const { container, rerender } = await render(
      <LiveGrid
        layout={4}
        activeCellId={0}
        maximizedCellId={null}
        cells={dummyCells}
        onSelectCell={vi.fn()}
        onClearCell={vi.fn()}
        onToggleMaximizeCell={vi.fn()}
        onSetCellMute={vi.fn()}
        onSetCellStreamType={vi.fn()}
      />
    )

    // 切换到 cellId = 1 最大化
    await rerender(
      <LiveGrid
        layout={4}
        activeCellId={1}
        maximizedCellId={1}
        cells={dummyCells}
        onSelectCell={vi.fn()}
        onClearCell={vi.fn()}
        onToggleMaximizeCell={vi.fn()}
        onSetCellMute={vi.fn()}
        onSetCellStreamType={vi.fn()}
      />
    )

    const cellWrappers = container.querySelectorAll('.grid > div.h-full')
    // 关键断言：4 个格子依然全在 DOM 中（不断连不销毁），而不是被 unmount 成只有 1 个
    expect(cellWrappers.length).toBe(4)

    // cell 0, 2, 3 应该带 hidden
    expect(cellWrappers[0].classList.contains('hidden')).toBe(true)
    expect(cellWrappers[2].classList.contains('hidden')).toBe(true)
    expect(cellWrappers[3].classList.contains('hidden')).toBe(true)

    // cell 1 应该最大化且不带 hidden
    expect(cellWrappers[1].classList.contains('hidden')).toBe(false)
    expect(cellWrappers[1].classList.contains('absolute')).toBe(true)
    expect(cellWrappers[1].classList.contains('inset-0')).toBe(true)
  })

  it('退出最大化时，所有单元格无缝恢复显示', async () => {
    const { container, rerender } = await render(
      <LiveGrid
        layout={4}
        activeCellId={1}
        maximizedCellId={1}
        cells={dummyCells}
        onSelectCell={vi.fn()}
        onClearCell={vi.fn()}
        onToggleMaximizeCell={vi.fn()}
        onSetCellMute={vi.fn()}
        onSetCellStreamType={vi.fn()}
      />
    )

    // 退出最大化
    await rerender(
      <LiveGrid
        layout={4}
        activeCellId={1}
        maximizedCellId={null}
        cells={dummyCells}
        onSelectCell={vi.fn()}
        onClearCell={vi.fn()}
        onToggleMaximizeCell={vi.fn()}
        onSetCellMute={vi.fn()}
        onSetCellStreamType={vi.fn()}
      />
    )

    const cellWrappers = container.querySelectorAll('.grid > div.h-full')
    expect(cellWrappers.length).toBe(4)
    cellWrappers.forEach((wrapper) => {
      expect(wrapper.classList.contains('hidden')).toBe(false)
      expect(wrapper.classList.contains('absolute')).toBe(false)
    })
  })
})
