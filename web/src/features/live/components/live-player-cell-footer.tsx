import { Camera, Maximize2, Minimize2, Volume2, VolumeX } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { type LiveCellState, type StreamStats } from '../types'
import { LiveStreamStats } from './live-stream-stats'

interface LivePlayerCellFooterProps {
  cell: LiveCellState
  stats: StreamStats
  isMicro: boolean
  isMaximized: boolean
  useMockCanvas: boolean
  onScreenshot: () => void
  onToggleMute: () => void
  onToggleMaximize: () => void
}

export function LivePlayerCellFooter({
  cell,
  stats,
  isMicro,
  isMaximized,
  useMockCanvas,
  onScreenshot,
  onToggleMute,
  onToggleMaximize,
}: LivePlayerCellFooterProps): React.JSX.Element {
  const { t } = useTranslation('live')

  const displayedStats: StreamStats = useMockCanvas
    ? {
        fps: 25,
        resolution: '1280x720',
        bitrateKbps: 1536,
        decoderMode: 'Mock',
      }
    : stats

  return (
    <div className='pointer-events-none absolute inset-x-2 bottom-2 z-20 flex items-center justify-between opacity-0 transition-all duration-200 group-hover:opacity-100'>
      {/* 左侧 OSD 胶囊 */}
      <div className='pointer-events-auto'>
        <LiveStreamStats compact={isMicro} stats={displayedStats} />
      </div>

      {/* 右侧动作控制胶囊 */}
      <div className='pointer-events-auto flex items-center gap-0.5 rounded-lg border border-border/40 bg-background/85 p-0.5 shadow-xs backdrop-blur-md'>
        <Button
          variant='ghost'
          size='icon'
          title={t('screenshot')}
          className={`${
            isMicro ? 'h-5 w-5' : 'h-6 w-6'
          } text-muted-foreground hover:text-foreground`}
          onClick={(e) => {
            e.stopPropagation()
            onScreenshot()
          }}
        >
          <Camera className={isMicro ? 'h-3 w-3' : 'h-3.5 w-3.5'} />
        </Button>

        <Button
          variant='ghost'
          size='icon'
          title={cell.isMuted ? t('unmute') : t('mute')}
          className={`${
            isMicro ? 'h-5 w-5' : 'h-6 w-6'
          } text-muted-foreground hover:text-foreground`}
          onClick={(e) => {
            e.stopPropagation()
            onToggleMute()
          }}
        >
          {cell.isMuted ? (
            <VolumeX className={isMicro ? 'h-3 w-3' : 'h-3.5 w-3.5'} />
          ) : (
            <Volume2
              className={`${
                isMicro ? 'h-3 w-3' : 'h-3.5 w-3.5'
              } text-emerald-500`}
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
          className={`${
            isMicro ? 'h-5 w-5' : 'h-6 w-6'
          } text-muted-foreground hover:text-foreground`}
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
  )
}
