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

export interface UseLiveGridReturn {
  layout: GridLayout
  setLayout: (newLayout: GridLayout) => void
  activeCellId: number
  setActiveCellId: (cellId: number) => void
  maximizedCellId: number | null
  toggleMaximizeCell: (cellId: number) => void
  resetMaximize: () => void
  cells: LiveCellState[]
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

export function useLiveGrid(): UseLiveGridReturn {
  const [layout, setLayout] = useState<GridLayout>(4)
  const [activeCellId, setActiveCellId] = useState<number>(0)
  const [maximizedCellId, setMaximizedCellId] = useState<number | null>(null)
  const [cells, setCells] = useState<LiveCellState[]>(INITIAL_CELLS)

  const handleSetLayout = useCallback((newLayout: GridLayout) => {
    setLayout(newLayout)
    setMaximizedCellId(null)
    setActiveCellId((current) => (current < newLayout ? current : 0))
  }, [])

  const assignCamera = useCallback(
    (
      cellId: number,
      camera: LiveCameraItem,
      streamType: StreamType = 'main'
    ) => {
      setCells((prev) =>
        prev.map((cell) =>
          cell.cellId === cellId
            ? {
                ...cell,
                cameraId: camera.id,
                cameraName: camera.name,
                streamType,
              }
            : cell
        )
      )
    },
    []
  )

  const clearCell = useCallback((cellId: number) => {
    setCells((prev) =>
      prev.map((cell) =>
        cell.cellId === cellId
          ? {
              ...cell,
              cameraId: null,
              cameraName: undefined,
            }
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

  const setCellMute = useCallback((cellId: number, isMuted: boolean) => {
    setCells((prev) =>
      prev.map((cell) => (cell.cellId === cellId ? { ...cell, isMuted } : cell))
    )
  }, [])

  const setCellStreamType = useCallback(
    (cellId: number, streamType: StreamType) => {
      setCells((prev) =>
        prev.map((cell) =>
          cell.cellId === cellId ? { ...cell, streamType } : cell
        )
      )
    },
    []
  )

  const muteAll = useCallback(() => {
    setCells((prev) => prev.map((cell) => ({ ...cell, isMuted: true })))
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
    assignCamera,
    clearCell,
    clearAllCells,
    setCellMute,
    setCellStreamType,
    muteAll,
  }
}
