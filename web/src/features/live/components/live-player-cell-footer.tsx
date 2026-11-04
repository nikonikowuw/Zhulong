import { Camera, Maximize2, Minimize2, Volume2, VolumeX } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { type LiveCellState, type StreamStats } from '../types'
import { LiveStreamStats } from './live-stream-stats'

interface LivePlayerCellFooterProps {
  cell: LiveCellState
  stats: StreamStats
  isMicro: boolean
  isMaximized: boolean
  /** 喇叭置灰（该路无音频 / 浏览器不支持该音频格式）。 */
  audioDisabled?: boolean
  /** 置灰时展示的说明文案（作为 title，禁用状态下用户也能知道原因）。 */
  audioDisabledReason?: string | null
  onScreenshot: () => void
  onToggleMute: () => void
  onToggleMaximize: () => void
}

export function LivePlayerCellFooter({
  cell,
  stats,
  isMicro,
  isMaximized,
  audioDisabled = false,
  audioDisabledReason = null,
  onScreenshot,
  onToggleMute,
  onToggleMaximize,
}: LivePlayerCellFooterProps): React.JSX.Element {
  const { t } = useTranslation('live')

  return (
    <div className='pointer-events-none absolute inset-x-2 bottom-2 z-20 flex items-center justify-between select-none'>
      {/* 左侧 OSD 胶囊：始终保持半透明常驻，hover 时更加清晰 */}
      <div className='pointer-events-auto opacity-75 transition-opacity duration-200 group-hover:opacity-100'>
        <LiveStreamStats compact={isMicro} stats={stats} />
      </div>

      {/* 右侧动作控制胶囊：hover 时平滑淡入呈现 */}
      <div className='pointer-events-auto flex items-center gap-0.5 rounded-lg border border-white/15 bg-black/70 p-0.5 opacity-0 shadow-md backdrop-blur-md transition-opacity duration-200 group-hover:opacity-100'>
        <Button
          variant='ghost'
          size='icon'
          title={t('screenshot')}
          className={cn(
            isMicro ? 'h-5 w-5' : 'h-6 w-6',
            'text-white/75 hover:bg-white/15 hover:text-white'
          )}
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
          // 置灰时文案优先：禁用状态的按钮拿不到 hover，title 是用户唯一能看到原因的地方。
          title={
            audioDisabled
              ? (audioDisabledReason ?? t('noAudio'))
              : cell.isMuted
                ? t('unmute')
                : t('mute')
          }
          aria-label={
            audioDisabled
              ? (audioDisabledReason ?? t('noAudio'))
              : cell.isMuted
                ? t('unmute')
                : t('mute')
          }
          aria-pressed={!cell.isMuted}
          disabled={audioDisabled}
          className={cn(
            isMicro ? 'h-5 w-5' : 'h-6 w-6',
            'text-white/75 hover:bg-white/15 hover:text-white',
            audioDisabled &&
              'cursor-not-allowed opacity-40 hover:bg-transparent'
          )}
          onClick={(e) => {
            e.stopPropagation()
            onToggleMute()
          }}
        >
          {cell.isMuted ? (
            <VolumeX className={isMicro ? 'h-3 w-3' : 'h-3.5 w-3.5'} />
          ) : (
            <Volume2
              className={cn(
                isMicro ? 'h-3 w-3' : 'h-3.5 w-3.5',
                'text-emerald-400'
              )}
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
          className={cn(
            isMicro ? 'h-5 w-5' : 'h-6 w-6',
            'text-white/75 hover:bg-white/15 hover:text-white'
          )}
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
