import { useRef, useState } from 'react'
import {
  Camera,
  Maximize2,
  Minimize2,
  Volume2,
  VolumeX,
  X,
  RotateCcw,
  Loader2,
  Tv,
  ChevronDown,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { getCameraStreamWsUrl } from '../api/live-camera-api'
import { useStreamConnection } from '../hooks/use-stream-connection'
import {
  type GridLayout,
  type LiveCameraItem,
  type LiveCellState,
  type StreamType,
} from '../types'
import { JessibucaPlayer, type JessibucaPlayerRef } from './jessibuca-player'
import { LiveStreamStats } from './live-stream-stats'
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

function getEmptyCellBorderClass(
  isDragOver: boolean,
  isActive: boolean
): string {
  if (isDragOver) {
    return 'border-primary bg-primary/10 shadow-[inset_0_0_0_1px_hsl(var(--primary))]'
  }
  if (isActive) {
    return 'border-primary bg-card/95 shadow-[inset_0_0_0_1px_hsl(var(--primary))]'
  }
  return 'border-border/60 bg-card/30 hover:border-border hover:bg-card/60'
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

function getIconContainerSize(isMicro: boolean, isCompact: boolean): string {
  if (isMicro) return 'h-6 w-6 rounded-md'
  if (isCompact) return 'h-8 w-8 rounded-lg'
  return 'h-10 w-10 rounded-xl'
}

function getCameraIconSize(isMicro: boolean, isCompact: boolean): string {
  if (isMicro) return 'h-3.5 w-3.5'
  if (isCompact) return 'h-4 w-4'
  return 'h-5 w-5'
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
  const jessibucaRef = useRef<JessibucaPlayerRef | null>(null)
  const mockCanvasRef = useRef<MockStreamCanvasRef | null>(null)

  const { status, errorMessage, stats, retry } = useStreamConnection({
    cameraId: cell.cameraId,
    streamType: cell.streamType,
    enabled: !useMockCanvas && Boolean(cell.cameraId),
  })

  const handleScreenshot = () => {
    if (useMockCanvas) {
      mockCanvasRef.current?.screenshot(cell.cameraName)
    } else {
      jessibucaRef.current?.screenshot(cell.cameraName)
    }
  }

  const handleToggleMute = () => {
    const nextMute = !cell.isMuted
    onSetMute(nextMute)
    jessibucaRef.current?.setMute(nextMute)
  }

  const handleDragOver = (e: React.DragEvent) => {
    e.preventDefault()
    e.dataTransfer.dropEffect = 'copy'
    if (!isDragOver) {
      setIsDragOver(true)
    }
  }

  const handleDragLeave = () => {
    setIsDragOver(false)
  }

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault()
    setIsDragOver(false)
    try {
      const raw = e.dataTransfer.getData('application/json')
      if (raw) {
        const cam = JSON.parse(raw) as LiveCameraItem
        if (cam && cam.id) {
          onDropCamera?.(cam)
        }
      }
    } catch {
      // 忽略外部无法解析的拖拽
    }
  }

  // 根据宫格密度计算紧凑程度
  const isMicro = layout === 16
  const isCompact = layout === 9 || isMicro

  // 1. 空窗状态 (干净整洁的实线视窗网格，边框采用内置式 border-2，杜绝任何外部裁切)
  if (!cell.cameraId) {
    return (
      <div
        onClick={onSelect}
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
        className={`group relative flex h-full w-full cursor-pointer flex-col items-center justify-center overflow-hidden rounded-xl border-2 transition-all select-none ${getEmptyCellBorderClass(
          isDragOver,
          isActive
        )}`}
      >
        {/* 通道角标 (仅在宽裕模式下显示在右上角，激活时点亮) */}
        {!isMicro && (
          <div className='absolute top-2 right-2 sm:top-2.5 sm:right-2.5'>
            <Badge
              variant={isActive ? 'default' : 'outline'}
              className={`font-mono text-[10px] ${
                isActive
                  ? 'bg-primary text-primary-foreground'
                  : 'text-muted-foreground'
              }`}
            >
              #{cell.cellId + 1}
            </Badge>
          </div>
        )}

        {/* 交互引导中心区 (极简纯净，无多余按钮与冗长文本) */}
        <div
          className={`pointer-events-none flex flex-col items-center text-center ${
            isMicro ? 'gap-1 p-1' : 'gap-1.5 p-2 sm:p-3'
          }`}
        >
          <div
            className={`flex items-center justify-center rounded-lg border transition-all duration-200 group-hover:scale-105 ${getIconContainerSize(
              isMicro,
              isCompact
            )} ${
              isActive
                ? 'border-primary bg-primary/15 text-primary'
                : 'border-border/80 bg-muted/60 text-muted-foreground group-hover:border-primary/40 group-hover:bg-primary/10 group-hover:text-primary'
            }`}
          >
            <Camera className={getCameraIconSize(isMicro, isCompact)} />
          </div>

          <div>
            <p
              className={`font-semibold ${
                isActive ? 'text-primary' : 'text-foreground'
              } ${isMicro ? 'text-[11px]' : 'text-xs'}`}
            >
              {isDragOver
                ? t('releaseToPlay')
                : `#${cell.cellId + 1} · ${t('idle', { defaultValue: 'Idle' })}`}
            </p>
            {!isCompact && (
              <p className='mt-0.5 text-[11px] text-muted-foreground'>
                {t('dragCameraToPlay', {
                  defaultValue: '拖拽左侧设备到此窗口播放',
                })}
              </p>
            )}
          </div>
        </div>
      </div>
    )
  }

  // 2. 正常播放或带流窗口
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
      {/* 拖拽悬浮覆盖层 */}
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
          <JessibucaPlayer
            ref={jessibucaRef}
            url={getCameraStreamWsUrl(cell.cameraId, cell.streamType)}
            isMuted={cell.isMuted}
          />
        )}
      </div>

      {/* 状态与加载指示遮罩 (毛玻璃优雅质感) */}
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

      {/* 顶部悬浮控制岛 (Frosted Island Bar) */}
      <div
        className={`absolute inset-x-2 top-2 z-20 flex items-center justify-between rounded-lg border border-border/40 bg-background/85 text-foreground opacity-0 shadow-xs backdrop-blur-md transition-all duration-200 group-hover:opacity-100 ${
          isMicro ? 'px-1.5 py-0.5 text-[10px]' : 'px-2.5 py-1.5 text-xs'
        }`}
      >
        <div className='flex items-center gap-1 font-medium'>
          <span className='h-2 w-2 animate-pulse rounded-full bg-emerald-500' />
          <span
            className={`truncate ${isMicro ? 'max-w-[70px]' : 'max-w-[130px] sm:max-w-[180px]'}`}
          >
            {cell.cameraName || t('cameraFallback', { id: cell.cameraId })}
          </span>
          <Badge variant='secondary' className='px-1 py-0 font-mono text-[9px]'>
            #{cell.cellId + 1}
          </Badge>
        </div>

        <div className='flex items-center gap-0.5'>
          {/* 主/子流切换 */}
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant='ghost'
                size='sm'
                className={`gap-0.5 text-muted-foreground hover:text-foreground ${
                  isMicro ? 'h-5 px-1 text-[10px]' : 'h-6 px-1.5 text-[11px]'
                }`}
                onClick={(e) => e.stopPropagation()}
              >
                <span>
                  {cell.streamType === 'main'
                    ? t('mainStream')
                    : t('subStream')}
                </span>
                <ChevronDown className='h-2.5 w-2.5 opacity-50' />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align='end' className='min-w-[100px]'>
              <DropdownMenuItem onClick={() => onSetStreamType('main')}>
                {t('mainStreamHd')}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => onSetStreamType('sub')}>
                {t('subStreamSd')}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>

          {/* 彩条测试图切换 */}
          <Button
            variant='ghost'
            size='icon'
            title={t('toggleMockStream')}
            className={`${isMicro ? 'h-5 w-5' : 'h-6 w-6'} ${useMockCanvas ? 'text-purple-500' : 'text-muted-foreground hover:text-foreground'}`}
            onClick={(e) => {
              e.stopPropagation()
              setUseMockCanvas(!useMockCanvas)
            }}
          >
            <Tv className={isMicro ? 'h-3 w-3' : 'h-3.5 w-3.5'} />
          </Button>

          {/* 关闭当前视频 */}
          <Button
            variant='ghost'
            size='icon'
            title={t('closeStream')}
            className={`${isMicro ? 'h-5 w-5' : 'h-6 w-6'} text-muted-foreground hover:bg-destructive/10 hover:text-destructive`}
            onClick={(e) => {
              e.stopPropagation()
              onClear()
            }}
          >
            <X className={isMicro ? 'h-3 w-3' : 'h-3.5 w-3.5'} />
          </Button>
        </div>
      </div>

      {/* 底部悬浮控制岛 (Frosted Island Bar) */}
      <div className='pointer-events-none absolute inset-x-2 bottom-2 z-20 flex items-center justify-between opacity-0 transition-all duration-200 group-hover:opacity-100'>
        {/* 左侧 OSD 胶囊 */}
        <div className='pointer-events-auto'>
          <LiveStreamStats
            compact={isMicro}
            stats={
              useMockCanvas
                ? {
                    fps: 25,
                    resolution: '1280x720',
                    bitrateKbps: 1536,
                    decoderMode: 'Mock',
                  }
                : stats
            }
          />
        </div>

        {/* 右侧动作控制胶囊 */}
        <div className='pointer-events-auto flex items-center gap-0.5 rounded-lg border border-border/40 bg-background/85 p-0.5 shadow-xs backdrop-blur-md'>
          <Button
            variant='ghost'
            size='icon'
            title={t('screenshot')}
            className={`${isMicro ? 'h-5 w-5' : 'h-6 w-6'} text-muted-foreground hover:text-foreground`}
            onClick={(e) => {
              e.stopPropagation()
              handleScreenshot()
            }}
          >
            <Camera className={isMicro ? 'h-3 w-3' : 'h-3.5 w-3.5'} />
          </Button>

          <Button
            variant='ghost'
            size='icon'
            title={cell.isMuted ? t('unmute') : t('mute')}
            className={`${isMicro ? 'h-5 w-5' : 'h-6 w-6'} text-muted-foreground hover:text-foreground`}
            onClick={(e) => {
              e.stopPropagation()
              handleToggleMute()
            }}
          >
            {cell.isMuted ? (
              <VolumeX className={isMicro ? 'h-3 w-3' : 'h-3.5 w-3.5'} />
            ) : (
              <Volume2
                className={`${isMicro ? 'h-3 w-3' : 'h-3.5 w-3.5'} text-emerald-500`}
              />
            )}
          </Button>

          <Button
            variant='ghost'
            size='icon'
            title={
              isMaximized
                ? t('restoreGrid', { id: cell.cellId + 1 })
                : t('maximize')
            }
            className={`${isMicro ? 'h-5 w-5' : 'h-6 w-6'} text-muted-foreground hover:text-foreground`}
            onClick={(e) => {
              e.stopPropagation()
              onToggleMaximize()
            }}
          >
            {isMaximized ? (
              <Minimize2 className={isMicro ? 'h-3 w-3' : 'h-3.5 w-3.5'} />
            ) : (
              <Maximize2 className={isMicro ? 'h-3 w-3' : 'h-3.5 w-3.5'} />
            )}
          </Button>
        </div>
      </div>
    </div>
  )
}
