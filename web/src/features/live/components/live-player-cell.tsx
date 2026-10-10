import { useCallback, useRef, useState } from 'react'
import { Camera, Loader2, RotateCcw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { useStreamConnection } from '../hooks/use-stream-connection'
import { StreamConnectionPool } from '../services/stream-pool'
import {
  type GridLayout,
  type LiveCameraItem,
  type LiveCellState,
  type ParsedPacket,
  type StreamType,
} from '../types'
import { LivePlayer, type LivePlayerRef } from './live-player'
import { LivePlayerCellFooter } from './live-player-cell-footer'
import { LivePlayerCellHeader } from './live-player-cell-header'
import { LivePlayerEmptyCell } from './live-player-empty-cell'
import {
  MockStreamCanvas,
  type MockStreamCanvasRef,
} from './mock-stream-canvas'

interface LivePlayerCellProps {
  cell: LiveCellState
  layout?: GridLayout
  isActive: boolean
  isMaximized: boolean
  onSelect: () => void
  onClear: () => void
  onToggleMaximize: () => void
  onSetMute: (isMuted: boolean) => void
  onSetStreamType: (streamType: StreamType) => void
  onDropCamera?: (camera: LiveCameraItem) => void
}

function getActiveCellBorderClass(
  isDragOver: boolean,
  isActive: boolean
): string {
  if (isDragOver || isActive) {
    return 'border-primary shadow-[inset_0_0_0_1px_hsl(var(--primary))]'
  }
  return 'border-border/60 hover:border-border'
}

export function LivePlayerCell({
  cell,
  layout = 4,
  isActive,
  isMaximized,
  onSelect,
  onClear,
  onToggleMaximize,
  onSetMute,
  onSetStreamType,
  onDropCamera,
}: LivePlayerCellProps): React.JSX.Element {
  const { t } = useTranslation('live')
  const [useMockCanvas, setUseMockCanvas] = useState(false)
  const [isDragOver, setIsDragOver] = useState(false)
  const playerRef = useRef<LivePlayerRef | null>(null)
  const mockCanvasRef = useRef<MockStreamCanvasRef | null>(null)

  // 单例单次订阅：数据包由 useStreamConnection 接收并分发给渲染器
  const handlePacket = useCallback((packet: ParsedPacket) => {
    playerRef.current?.decode(packet)
  }, [])

  const { status, errorMessage, stats, retry } = useStreamConnection({
    cameraId: cell.cameraId,
    streamType: cell.streamType,
    enabled: !useMockCanvas && Boolean(cell.cameraId),
    onPacket: handlePacket,
  })

  // 播放器识别出实际视频分辨率时，回流更新连接池状态
  const handlePlayerStatsChange = useCallback(
    (newStats: { resolution?: string }) => {
      if (cell.cameraId && newStats.resolution) {
        StreamConnectionPool.getInstance().updateStats(
          cell.cameraId,
          cell.streamType,
          { resolution: newStats.resolution }
        )
      }
    },
    [cell.cameraId, cell.streamType]
  )

  const handleScreenshot = () => {
    if (useMockCanvas) {
      mockCanvasRef.current?.screenshot(cell.cameraName)
    } else {
      playerRef.current?.screenshot(cell.cameraName)
    }
  }

  const handleToggleMute = () => {
    const nextMute = !cell.isMuted
    onSetMute(nextMute)
    playerRef.current?.setMute(nextMute)
  }

  const handleDragOver = (e: React.DragEvent) => {
    e.preventDefault()
    e.dataTransfer.dropEffect = 'copy'
    if (!isDragOver) setIsDragOver(true)
  }

  const handleDragLeave = () => {
    setIsDragOver(false)
  }

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault()
    setIsDragOver(false)
    try {
      const dataStr = e.dataTransfer.getData('application/json')
      if (dataStr) {
        const camera = JSON.parse(dataStr) as LiveCameraItem
        if (camera && camera.id) {
          onDropCamera?.(camera)
        }
      }
    } catch {
      // 忽略非法拖拽数据
    }
  }

  const isMicro = layout === 16
  const isCompact = layout === 9 || isMicro

  if (!cell.cameraId) {
    return (
      <LivePlayerEmptyCell
        cell={cell}
        isActive={isActive}
        isDragOver={isDragOver}
        isMicro={isMicro}
        isCompact={isCompact}
        onSelect={onSelect}
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
      />
    )
  }

  return (
    <div
      onClick={onSelect}
      onDoubleClick={onToggleMaximize}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
      className={`group relative flex h-full w-full overflow-hidden rounded-xl border-2 bg-black text-white transition-all select-none ${getActiveCellBorderClass(
        isDragOver,
        isActive
      )}`}
    >
      {/* 拖拽释放高亮指示浮层 */}
      {isDragOver && (
        <div className='absolute inset-0 z-30 flex flex-col items-center justify-center bg-primary/20 backdrop-blur-xs'>
          <div className='animate-bounce rounded-full bg-primary/90 p-3 text-primary-foreground shadow-lg'>
            <Camera className='h-6 w-6' />
          </div>
          <p className='mt-2 text-xs font-semibold text-white drop-shadow'>
            {t('releaseToPlay')}
          </p>
        </div>
      )}

      {/* 视频画面主体 */}
      <div className='relative h-full w-full'>
        {useMockCanvas ? (
          <MockStreamCanvas
            ref={mockCanvasRef}
            cameraName={cell.cameraName}
            streamType={cell.streamType}
          />
        ) : (
          <LivePlayer
            ref={playerRef}
            isMuted={cell.isMuted}
            onStatsChange={handlePlayerStatsChange}
          />
        )}
      </div>

      {/* 状态与加载指示遮罩 */}
      {!useMockCanvas &&
        (status === 'connecting' || status === 'reconnecting') && (
          <div className='absolute inset-0 z-10 flex flex-col items-center justify-center bg-background/80 backdrop-blur-xs'>
            <Loader2 className='h-6 w-6 animate-spin text-primary' />
            <p className='mt-2 font-mono text-xs text-muted-foreground'>
              {status === 'connecting' ? t('connecting') : errorMessage}
            </p>
          </div>
        )}

      {/* 异常错误提示遮罩 */}
      {!useMockCanvas && status === 'error' && (
        <div className='absolute inset-0 z-10 flex flex-col items-center justify-center bg-background/90 p-4 text-center backdrop-blur-xs'>
          <div className='rounded-full bg-destructive/10 p-2 text-destructive'>
            <RotateCcw className='h-5 w-5' />
          </div>
          <p className='mt-2 text-xs font-medium text-destructive'>
            {errorMessage || t('streamInterrupted')}
          </p>
          <Button
            size='sm'
            variant='outline'
            onClick={(e) => {
              e.stopPropagation()
              retry()
            }}
            className='mt-3 h-7 text-xs'
          >
            {t('reconnect')}
          </Button>
        </div>
      )}

      {/* 顶部悬浮控制岛 */}
      <LivePlayerCellHeader
        cell={cell}
        isMicro={isMicro}
        useMockCanvas={useMockCanvas}
        onToggleMock={() => setUseMockCanvas(!useMockCanvas)}
        onSetStreamType={onSetStreamType}
        onClear={onClear}
      />

      {/* 底部悬浮控制岛 */}
      <LivePlayerCellFooter
        cell={cell}
        stats={stats}
        isMicro={isMicro}
        isMaximized={isMaximized}
        useMockCanvas={useMockCanvas}
        onScreenshot={handleScreenshot}
        onToggleMute={handleToggleMute}
        onToggleMaximize={onToggleMaximize}
      />
    </div>
  )
}
