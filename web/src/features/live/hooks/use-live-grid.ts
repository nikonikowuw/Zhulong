import { useState, useCallback } from 'react'
import {
  type GridLayout,
  type LiveCameraItem,
  type LiveCellState,
  type StreamType,
} from '../types'

const INITIAL_CELLS: LiveCellState[] = Array.from({ length: 16 }, (_, idx) => ({
  cellId: idx,
  cameraId: null,
  streamType: 'main',
  isMuted: true,
}))

/** 音量默认值。**不持久化**（父任务未决项），刷新即回到满音量。 */
const DEFAULT_VOLUME = 1

function clampVolume(volume: number): number {
  if (!Number.isFinite(volume)) return DEFAULT_VOLUME
  return Math.min(1, Math.max(0, volume))
}

export interface UseLiveGridReturn {
  layout: GridLayout
  setLayout: (newLayout: GridLayout) => void
  activeCellId: number
  setActiveCellId: (cellId: number) => void
  maximizedCellId: number | null
  toggleMaximizeCell: (cellId: number) => void
  resetMaximize: () => void
  cells: LiveCellState[]
  /** 出声音量 0..1。与「哪一路出声」正交：焦点决定谁出声，音量决定多大声。 */
  volume: number
  setVolume: (volume: number) => void
  assignCamera: (
    cellId: number,
    camera: LiveCameraItem,
    streamType?: StreamType
  ) => void
  clearCell: (cellId: number) => void
  clearAllCells: () => void
  setCellMute: (cellId: number, isMuted: boolean) => void
  setCellStreamType: (cellId: number, streamType: StreamType) => void
  muteAll: () => void
}

/**
 * 一路流的身份：`cameraId + streamType`。
 *
 * 同一台摄像机的主/子流是**两条独立的流**（不同的 WS、不同的 PTS 原点），但对用户
 * 来说是「同一路声音」。反过来，同一个 `cameraId` 的多分屏格子如果共用一路出声，
 * 按 streamType 区分反而会让主/子流同时响 —— 所以这里两者都带上：只有完全相同的
 * 流才被判为「同一路」。
 */
function streamKeyOf(cell: LiveCellState): string | null {
  if (!cell.cameraId) return null
  return `${cell.cameraId}:${cell.streamType}`
}

/**
 * 录制（焦点回收）：当操作可能改变某一格的流身份时，把该格静音。
 *
 * `isMuted` 是唯一真相源 —— 没有额外的 `activeCellId` 音频焦点状态需要清理，
 * 「谁在出声」就是「谁的 `isMuted === false`」。因此回收焦点 = 把那个格子置为静音。
 */
function muteCell(cell: LiveCellState): LiveCellState {
  return cell.isMuted ? cell : { ...cell, isMuted: true }
}

export function useLiveGrid(): UseLiveGridReturn {
  const [layout, setLayout] = useState<GridLayout>(1)
  const [activeCellId, setActiveCellId] = useState<number>(0)
  const [maximizedCellId, setMaximizedCellId] = useState<number | null>(null)
  const [cells, setCells] = useState<LiveCellState[]>(INITIAL_CELLS)
  const [volume, setVolumeState] = useState<number>(DEFAULT_VOLUME)

  const handleSetLayout = useCallback((newLayout: GridLayout) => {
    setLayout(newLayout)
    setMaximizedCellId(null)
    setActiveCellId((current) => (current < newLayout ? current : 0))
  }, [])

  const setVolume = useCallback((next: number) => {
    setVolumeState(clampVolume(next))
  }, [])

  const assignCamera = useCallback(
    (cellId: number, camera: LiveCameraItem, streamType?: StreamType) => {
      const resolvedStreamType: StreamType =
        streamType ?? (layout > 1 && camera.hasSubStream ? 'sub' : 'main')
      setCells((prev) =>
        prev.map((cell) => {
          if (cell.cellId !== cellId) return cell
          const next: LiveCellState = {
            ...cell,
            cameraId: camera.id,
            cameraName: camera.name,
            hasSubStream: camera.hasSubStream,
            streamType: resolvedStreamType,
          }
          // 换了摄像机 / 换了码流 = 换了流身份：旧焦点指向的声音已经不存在了。
          // 无论同不同都要重算一遍 key，避免「换成同一台机但码流变了」漏判。
          const before = streamKeyOf(cell)
          const after = streamKeyOf(next)
          if (!cell.isMuted && before !== after) return muteCell(next)
          return next
        })
      )
    },
    [layout]
  )

  const clearCell = useCallback((cellId: number) => {
    setCells((prev) =>
      prev.map((cell) =>
        cell.cellId === cellId
          ? muteCell({
              ...cell,
              cameraId: null,
              cameraName: undefined,
              hasSubStream: undefined,
            })
          : cell
      )
    )
  }, [])

  const clearAllCells = useCallback(() => {
    setCells(INITIAL_CELLS)
  }, [])

  const toggleMaximizeCell = useCallback((cellId: number) => {
    setMaximizedCellId((prev) => (prev === cellId ? null : cellId))
  }, [])

  const resetMaximize = useCallback(() => {
    setMaximizedCellId(null)
  }, [])

  /**
   * 设置某格静音。`isMuted = false` 是**互斥**的：
   *
   *   1. 同一 tab 最多一路出声 → 其它所有格子一律置静音；
   *   2. 同一路流的其它格子也置静音 → 同一路流在多个分屏里仍只出声一次
   *      （两条 `connectAudioChannel` 同时连同一路会各建一条 WS，不但双份流量，
   *      还会因为两条音频通道的重连退避不同步而听到回声）。
   *
   * `isMuted = true` 没有额外副作用：`isMuted` 本身就是真相源，「取消焦点」就是
   * 「把这一格置静音」，不需要再维护一个 `activeCellId`。
   */
  const setCellMute = useCallback((cellId: number, isMuted: boolean) => {
    setCells((prev) => {
      const target = prev.find((cell) => cell.cellId === cellId)
      if (!target) return prev

      if (isMuted) {
        // 已经是静音：返回原数组引用，避免一次无意义的重渲染。
        if (target.isMuted) return prev
        return prev.map((cell) =>
          cell.cellId === cellId ? { ...cell, isMuted: true } : cell
        )
      }

      const targetKey = streamKeyOf(target)
      return prev.map((cell) => {
        if (cell.cellId === cellId) return { ...cell, isMuted: false }
        // 同一路流：即使不是目标格也必须静音。
        if (targetKey !== null && streamKeyOf(cell) === targetKey) {
          return { ...cell, isMuted: true }
        }
        // 其它所有格子：单路出声互斥。
        return cell.isMuted ? cell : { ...cell, isMuted: true }
      })
    })
  }, [])

  const setCellStreamType = useCallback(
    (cellId: number, streamType: StreamType) => {
      setCells((prev) =>
        prev.map((cell) => {
          if (cell.cellId !== cellId) return cell
          if (streamType === 'sub' && !cell.hasSubStream) {
            return cell
          }
          // 已经是这个码流：什么都没变，别把正在出声的格子误伤成静音
          // （顶部下拉里再点一次当前项是很自然的操作）。
          if (cell.streamType === streamType) return cell
          // 码流换了：原来出声的那一路已经不存在，焦点回收（静音）。
          return muteCell({ ...cell, streamType })
        })
      )
    },
    []
  )

  const muteAll = useCallback(() => {
    setCells((prev) =>
      prev.every((cell) => cell.isMuted)
        ? prev
        : prev.map((cell) => ({ ...cell, isMuted: true }))
    )
  }, [])

  return {
    layout,
    setLayout: handleSetLayout,
    activeCellId,
    setActiveCellId,
    maximizedCellId,
    toggleMaximizeCell,
    resetMaximize,
    cells,
    volume,
    setVolume,
    assignCamera,
    clearCell,
    clearAllCells,
    setCellMute,
    setCellStreamType,
    muteAll,
  }
}
