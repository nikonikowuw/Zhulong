import {
  type GridLayout,
  type LiveCameraItem,
  type LiveCellState,
  type StreamType,
} from '../types'
import { LivePlayerCell } from './live-player-cell'

const GRID_LAYOUT_CLASSES: Record<GridLayout, string> = {
  1: 'grid-cols-1 grid-rows-1',
  4: 'grid-cols-2 grid-rows-2',
  9: 'grid-cols-3 grid-rows-3',
  16: 'grid-cols-4 grid-rows-4',
}

interface LiveGridProps {
  layout: GridLayout
  activeCellId: number
  maximizedCellId: number | null
  cells: LiveCellState[]
  onSelectCell: (cellId: number) => void
  onClearCell: (cellId: number) => void
  onToggleMaximizeCell: (cellId: number) => void
  onSetCellMute: (cellId: number, isMuted: boolean) => void
  onSetCellStreamType: (cellId: number, streamType: StreamType) => void
  onDropCameraToCell?: (cellId: number, camera: LiveCameraItem) => void
}

export function LiveGrid({
  layout,
  activeCellId,
  maximizedCellId,
  cells,
  onSelectCell,
  onClearCell,
  onToggleMaximizeCell,
  onSetCellMute,
  onSetCellStreamType,
  onDropCameraToCell,
}: LiveGridProps): React.JSX.Element {
  // 单窗最大化展示
  if (maximizedCellId !== null) {
    const cell = cells.find((c) => c.cellId === maximizedCellId) ?? cells[0]
    return (
      <div className='relative h-full w-full'>
        <LivePlayerCell
          cell={cell}
          layout={1}
          isActive={true}
          isMaximized={true}
          onSelect={() => onSelectCell(cell.cellId)}
          onClear={() => onClearCell(cell.cellId)}
          onToggleMaximize={() => onToggleMaximizeCell(cell.cellId)}
          onSetMute={(muted) => onSetCellMute(cell.cellId, muted)}
          onSetStreamType={(type) => onSetCellStreamType(cell.cellId, type)}
          onDropCamera={(cam) => onDropCameraToCell?.(cell.cellId, cam)}
        />
      </div>
    )
  }

  // 计算当前网格需要展示的单元格列表 (截取前 layout 个)
  const visibleCells = cells.slice(0, layout)

  return (
    <div className='relative h-full w-full overflow-hidden'>
      <div
        className={`grid h-full w-full gap-1.5 sm:gap-2.5 ${GRID_LAYOUT_CLASSES[layout]}`}
      >
        {visibleCells.map((cell) => (
          <div key={cell.cellId} className='h-full min-h-0 w-full min-w-0'>
            <LivePlayerCell
              cell={cell}
              layout={layout}
              isActive={cell.cellId === activeCellId}
              isMaximized={false}
              onSelect={() => onSelectCell(cell.cellId)}
              onClear={() => onClearCell(cell.cellId)}
              onToggleMaximize={() => onToggleMaximizeCell(cell.cellId)}
              onSetMute={(muted) => onSetCellMute(cell.cellId, muted)}
              onSetStreamType={(type) => onSetCellStreamType(cell.cellId, type)}
              onDropCamera={(cam) => onDropCameraToCell?.(cell.cellId, cam)}
            />
          </div>
        ))}
      </div>
    </div>
  )
}
