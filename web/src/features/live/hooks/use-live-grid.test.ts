import { describe, it, expect } from 'vitest'
import { renderHook } from 'vitest-browser-react'
import { type LiveCameraItem } from '../types'
import { useLiveGrid } from './use-live-grid'

describe('useLiveGrid', () => {
  const dummyCamera: LiveCameraItem = {
    id: 'cam-01',
    name: '测试相机',
    host: '192.168.1.100',
    status: 'online',
    hasSubStream: true,
    codec: 'h264',
  }

  it('初始状态为单窗口且 cellId 0 处于激活态', async () => {
    const { result } = await renderHook(() => useLiveGrid())
    expect(result.current.layout).toBe(1)
    expect(result.current.activeCellId).toBe(0)
    expect(result.current.maximizedCellId).toBeNull()
    expect(result.current.cells.length).toBe(16)
  })

  it('支持切换分屏布局', async () => {
    const { result, act } = await renderHook(() => useLiveGrid())

    await act(() => {
      result.current.setLayout(9)
    })
    expect(result.current.layout).toBe(9)

    await act(() => {
      result.current.setLayout(1)
    })
    expect(result.current.layout).toBe(1)
    expect(result.current.activeCellId).toBe(0)
  })

  it('支持分配与清空摄像机', async () => {
    const { result, act } = await renderHook(() => useLiveGrid())

    await act(() => {
      result.current.assignCamera(1, dummyCamera, 'main')
    })
    expect(result.current.cells[1].cameraId).toBe('cam-01')
    expect(result.current.cells[1].cameraName).toBe('测试相机')

    await act(() => {
      result.current.clearCell(1)
    })
    expect(result.current.cells[1].cameraId).toBeNull()
  })

  it('多分屏下未指定码流时自动选择子流，单分屏下选择主流', async () => {
    const { result, act } = await renderHook(() => useLiveGrid())

    await act(() => {
      result.current.setLayout(4)
    })
    await act(() => {
      result.current.assignCamera(0, dummyCamera)
    })
    expect(result.current.cells[0].streamType).toBe('sub')

    await act(() => {
      result.current.setLayout(1)
    })
    await act(() => {
      result.current.assignCamera(0, dummyCamera)
    })
    expect(result.current.cells[0].streamType).toBe('main')
  })

  it('相机无子流时多分屏仍选择主流，且禁止切换至子流', async () => {
    const singleStreamCamera: LiveCameraItem = {
      id: 'cam-single',
      name: '单流相机',
      status: 'online',
      hasSubStream: false,
      codec: 'h265',
    }
    const { result, act } = await renderHook(() => useLiveGrid())

    await act(() => {
      result.current.setLayout(4)
    })
    await act(() => {
      result.current.assignCamera(0, singleStreamCamera)
    })
    expect(result.current.cells[0].streamType).toBe('main')
    expect(result.current.cells[0].hasSubStream).toBe(false)

    await act(() => {
      result.current.setCellStreamType(0, 'sub')
    })
    expect(result.current.cells[0].streamType).toBe('main')
  })

  it('支持单窗放大与还原', async () => {
    const { result, act } = await renderHook(() => useLiveGrid())

    await act(() => {
      result.current.toggleMaximizeCell(2)
    })
    expect(result.current.maximizedCellId).toBe(2)

    await act(() => {
      result.current.toggleMaximizeCell(2)
    })
    expect(result.current.maximizedCellId).toBeNull()
  })

  it('支持一键静音与一键清空', async () => {
    const { result, act } = await renderHook(() => useLiveGrid())

    await act(() => {
      result.current.setCellMute(0, false)
    })
    expect(result.current.cells[0].isMuted).toBe(false)

    await act(() => {
      result.current.muteAll()
    })
    expect(result.current.cells[0].isMuted).toBe(true)

    await act(() => {
      result.current.assignCamera(0, dummyCamera)
      result.current.clearAllCells()
    })
    expect(result.current.cells[0].cameraId).toBeNull()
  })
})
