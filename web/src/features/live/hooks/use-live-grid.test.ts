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

/**
 * 音频焦点（`isMuted` 是唯一真相源）。
 *
 * 这里的每条规则都对应父任务的一条验收：单 tab 最多一路出声、同一路流多个格子仍只一路、
 * 换机/清空/切码流后焦点回收、音量钳制。
 */
describe('useLiveGrid 音频焦点', () => {
  const camA: LiveCameraItem = {
    id: 'cam-a',
    name: 'A 枪机',
    host: '10.0.0.1',
    status: 'online',
    hasSubStream: true,
    codec: 'h264',
  }
  const camB: LiveCameraItem = {
    id: 'cam-b',
    name: 'B 球机',
    host: '10.0.0.2',
    status: 'online',
    hasSubStream: true,
    codec: 'h264',
  }

  /** 在 4 分屏下给若干格子分配摄像机（默认走子流）。 */
  async function setup(
    assignments: Array<{ cellId: number; camera: LiveCameraItem }>
  ) {
    const rendered = await renderHook(() => useLiveGrid())
    await rendered.act(() => {
      rendered.result.current.setLayout(4)
    })
    for (const item of assignments) {
      await rendered.act(() => {
        rendered.result.current.assignCamera(item.cellId, item.camera)
      })
    }
    return rendered
  }

  it('初始全静音，音量为 1', async () => {
    const { result } = await renderHook(() => useLiveGrid())
    expect(result.current.cells.every((cell) => cell.isMuted)).toBe(true)
    expect(result.current.volume).toBe(1)
  })

  it('解禁一格会静音其它所有格子（互斥）', async () => {
    const { result, act } = await setup([
      { cellId: 0, camera: camA },
      { cellId: 1, camera: camB },
    ])

    await act(() => {
      result.current.setCellMute(0, false)
    })
    expect(result.current.cells[0].isMuted).toBe(false)
    expect(result.current.cells[1].isMuted).toBe(true)

    await act(() => {
      result.current.setCellMute(1, false)
    })
    expect(result.current.cells[0].isMuted).toBe(true)
    expect(result.current.cells[1].isMuted).toBe(false)
  })

  it('同一路流（同 cameraId + streamType）的其它格子也被静音', async () => {
    const { result, act } = await setup([
      { cellId: 0, camera: camA },
      { cellId: 1, camera: camA },
      { cellId: 2, camera: camB },
    ])

    await act(() => {
      result.current.setCellMute(2, false)
    })
    expect(result.current.cells[2].isMuted).toBe(false)

    // 解禁格 0（与格 1 同一路流）：格 2 必须让位，格 1 也必须静音
    await act(() => {
      result.current.setCellMute(0, false)
    })
    expect(result.current.cells[0].isMuted).toBe(false)
    expect(result.current.cells[1].isMuted).toBe(true)
    expect(result.current.cells[2].isMuted).toBe(true)

    const unmuted = result.current.cells.filter((cell) => !cell.isMuted)
    expect(unmuted).toHaveLength(1)
  })

  it('同一摄像机的主/子流是两条流：解禁其中一路不影响另一路的静音状态判定', async () => {
    const { result, act } = await setup([{ cellId: 0, camera: camA }])
    await act(() => {
      result.current.assignCamera(1, camA, 'main')
    })

    await act(() => {
      result.current.setCellMute(0, false)
    })
    expect(result.current.cells[0].isMuted).toBe(false)
    // 格 1 是 main 流（不同 stream key），但互斥规则照样把它压成静音
    expect(result.current.cells[1].isMuted).toBe(true)
  })

  it('静音当前出声格 = 取消焦点（无其它副作用）', async () => {
    const { result, act } = await setup([{ cellId: 0, camera: camA }])

    await act(() => {
      result.current.setCellMute(0, false)
    })
    expect(result.current.cells[0].isMuted).toBe(false)

    await act(() => {
      result.current.setCellMute(0, true)
    })
    expect(result.current.cells.every((cell) => cell.isMuted)).toBe(true)
  })

  it('muteAll 清空全部焦点', async () => {
    const { result, act } = await setup([
      { cellId: 0, camera: camA },
      { cellId: 1, camera: camB },
    ])

    await act(() => {
      result.current.setCellMute(1, false)
    })
    expect(result.current.cells[1].isMuted).toBe(false)

    await act(() => {
      result.current.muteAll()
    })
    expect(result.current.cells.every((cell) => cell.isMuted)).toBe(true)
  })

  it('换机回收焦点：给当前出声格换一台机 → 该格静音', async () => {
    const { result, act } = await setup([{ cellId: 0, camera: camA }])

    await act(() => {
      result.current.setCellMute(0, false)
    })
    expect(result.current.cells[0].isMuted).toBe(false)

    await act(() => {
      result.current.assignCamera(0, camB)
    })
    expect(result.current.cells[0].isMuted).toBe(true)
    expect(result.current.cells[0].cameraId).toBe('cam-b')
  })

  it('换成同一台机同一路流：焦点保留（流没变）', async () => {
    const { result, act } = await setup([{ cellId: 0, camera: camA }])

    await act(() => {
      result.current.setCellMute(0, false)
    })
    await act(() => {
      result.current.assignCamera(0, camA, 'sub')
    })
    expect(result.current.cells[0].isMuted).toBe(false)
  })

  it('清空当前出声格 → 焦点回收', async () => {
    const { result, act } = await setup([{ cellId: 0, camera: camA }])

    await act(() => {
      result.current.setCellMute(0, false)
    })
    await act(() => {
      result.current.clearCell(0)
    })
    expect(result.current.cells[0].isMuted).toBe(true)
    expect(result.current.cells[0].cameraId).toBeNull()
  })

  it('clearAllCells → 焦点回收', async () => {
    const { result, act } = await setup([{ cellId: 0, camera: camA }])

    await act(() => {
      result.current.setCellMute(0, false)
    })
    await act(() => {
      result.current.clearAllCells()
    })
    expect(result.current.cells.every((cell) => cell.isMuted)).toBe(true)
  })

  it('切码流回收焦点：当前出声格切 main/sub → 静音', async () => {
    const { result, act } = await setup([{ cellId: 0, camera: camA }])
    expect(result.current.cells[0].streamType).toBe('sub')

    await act(() => {
      result.current.setCellMute(0, false)
    })
    expect(result.current.cells[0].isMuted).toBe(false)

    await act(() => {
      result.current.setCellStreamType(0, 'main')
    })
    expect(result.current.cells[0].streamType).toBe('main')
    expect(result.current.cells[0].isMuted).toBe(true)
  })

  it('切到无子流相机的 sub 是 no-op：不会误伤焦点', async () => {
    const noSub: LiveCameraItem = {
      id: 'cam-nosub',
      name: '单流相机',
      status: 'online',
      hasSubStream: false,
      codec: 'h264',
    }
    const { result, act } = await setup([{ cellId: 0, camera: noSub }])
    expect(result.current.cells[0].streamType).toBe('main')

    await act(() => {
      result.current.setCellMute(0, false)
    })
    await act(() => {
      result.current.setCellStreamType(0, 'sub')
    })
    // hasSubStream=false → 直接返回原 cell，焦点不变
    expect(result.current.cells[0].streamType).toBe('main')
    expect(result.current.cells[0].isMuted).toBe(false)
  })

  it('音量钳制到 0..1，非法值退回默认', async () => {
    const { result, act } = await renderHook(() => useLiveGrid())

    await act(() => {
      result.current.setVolume(0.35)
    })
    expect(result.current.volume).toBeCloseTo(0.35, 5)

    await act(() => {
      result.current.setVolume(1.8)
    })
    expect(result.current.volume).toBe(1)

    await act(() => {
      result.current.setVolume(-2)
    })
    expect(result.current.volume).toBe(0)

    await act(() => {
      result.current.setVolume(Number.NaN)
    })
    expect(result.current.volume).toBe(1)
  })
})
