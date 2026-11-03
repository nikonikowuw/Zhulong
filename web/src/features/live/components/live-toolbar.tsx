import { useState, useEffect } from 'react'
import {
  Square,
  Grid2X2,
  Grid3X3,
  LayoutGrid,
  Maximize,
  Minimize,
  VolumeX,
  Trash2,
  ScanFace,
  Sparkles,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { statusDotClass, statusLevelClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { Slider } from '@/components/ui/slider'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { type GridLayout } from '../types'

interface LiveToolbarProps {
  layout: GridLayout
  onLayoutChange: (layout: GridLayout) => void
  onMuteAll: () => void
  onClearAll: () => void
  activeStreamsCount: number
  /** 出声音量 0..1（不持久化）。 */
  volume?: number
  onVolumeChange?: (volume: number) => void
  showAIOverlay?: boolean
  onToggleAIOverlay?: () => void
  isFeedOpen?: boolean
  onToggleFeed?: () => void
}

const LAYOUT_OPTIONS: Array<{
  layout: GridLayout
  icon: typeof Square
  tooltipKey: 'singleView' | 'quadView' | 'nineView' | 'sixteenView'
  shortcut: string
}> = [
  { layout: 1, icon: Square, tooltipKey: 'singleView', shortcut: '1' },
  { layout: 4, icon: Grid2X2, tooltipKey: 'quadView', shortcut: '4' },
  { layout: 9, icon: Grid3X3, tooltipKey: 'nineView', shortcut: '9' },
  { layout: 16, icon: LayoutGrid, tooltipKey: 'sixteenView', shortcut: '' },
]

export function LiveToolbar({
  layout,
  onLayoutChange,
  onMuteAll,
  onClearAll,
  activeStreamsCount,
  volume = 1,
  onVolumeChange,
  showAIOverlay = true,
  onToggleAIOverlay,
  isFeedOpen = false,
  onToggleFeed,
}: LiveToolbarProps): React.JSX.Element {
  const { t } = useTranslation('live')
  const [isFullscreen, setIsFullscreen] = useState(false)

  useEffect(() => {
    const handleFullscreenChange = () => {
      setIsFullscreen(Boolean(document.fullscreenElement))
    }
    document.addEventListener('fullscreenchange', handleFullscreenChange)
    return () =>
      document.removeEventListener('fullscreenchange', handleFullscreenChange)
  }, [])

  const toggleBrowserFullscreen = () => {
    if (!document.fullscreenElement) {
      void document.documentElement.requestFullscreen()
    } else {
      void document.exitFullscreen()
    }
  }

  return (
    <TooltipProvider delayDuration={200}>
      <div className='flex items-center gap-1.5 select-none sm:gap-2'>
        {/* 活跃流状态 Badge */}
        <Badge
          variant='outline'
          className={cn(
            'hidden items-center gap-2 px-3 py-1 text-xs font-medium shadow-2xs sm:flex',
            statusLevelClass('active')
          )}
        >
          <span className='relative flex h-2 w-2'>
            {activeStreamsCount > 0 && (
              <span className='absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75' />
            )}
            <span
              className={cn(
                'relative inline-flex h-2 w-2 rounded-full',
                statusDotClass('active')
              )}
            />
          </span>
          <span className='font-mono font-semibold'>{activeStreamsCount}</span>
          <span className='text-muted-foreground'>{t('activeStreams')}</span>
        </Badge>

        {/* 宫格切换分段选择器 */}
        <div className='flex items-center rounded-lg border border-border/80 bg-muted/40 p-0.5 shadow-2xs'>
          {LAYOUT_OPTIONS.map((item) => {
            const Icon = item.icon
            const isSelected = layout === item.layout
            return (
              <Tooltip key={item.layout}>
                <TooltipTrigger asChild>
                  <Button
                    variant='ghost'
                    size='icon'
                    className={cn(
                      'h-7 w-7 rounded-md transition-all duration-150',
                      isSelected
                        ? 'bg-background font-semibold text-foreground shadow-xs'
                        : 'text-muted-foreground hover:bg-muted/70 hover:text-foreground'
                    )}
                    onClick={() => onLayoutChange(item.layout)}
                  >
                    <Icon className='h-3.5 w-3.5' />
                  </Button>
                </TooltipTrigger>
                <TooltipContent side='bottom' className='text-xs'>
                  <span>{t(item.tooltipKey)}</span>
                  {item.shortcut && (
                    <kbd className='ms-1.5 rounded border border-border/60 bg-muted px-1 py-0.5 font-mono text-[10px] text-muted-foreground'>
                      {item.shortcut}
                    </kbd>
                  )}
                </TooltipContent>
              </Tooltip>
            )
          })}
        </div>

        <Separator orientation='vertical' className='mx-0.5 h-5 opacity-60' />

        {/* AI 特性控制组 */}
        <div className='flex items-center gap-1'>
          {onToggleAIOverlay && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant='outline'
                  size='icon'
                  className={cn(
                    'h-8 w-8 shadow-2xs transition-all duration-150',
                    showAIOverlay
                      ? 'border-indigo-500/40 bg-indigo-500/10 text-indigo-500 shadow-[0_0_8px_rgba(99,102,241,0.15)] hover:bg-indigo-500/20 dark:text-indigo-400'
                      : 'text-muted-foreground hover:text-foreground'
                  )}
                  onClick={onToggleAIOverlay}
                >
                  <ScanFace className='h-4 w-4' />
                </Button>
              </TooltipTrigger>
              <TooltipContent side='bottom'>
                {showAIOverlay
                  ? t('disableAIOverlay', '关闭人脸框叠加')
                  : t('enableAIOverlay', '开启人脸框叠加')}
              </TooltipContent>
            </Tooltip>
          )}

          {onToggleFeed && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant='outline'
                  size='icon'
                  className={cn(
                    'h-8 w-8 shadow-2xs transition-all duration-150',
                    isFeedOpen
                      ? 'border-amber-500/40 bg-amber-500/10 text-amber-500 shadow-[0_0_8px_rgba(245,158,11,0.15)] hover:bg-amber-500/20 dark:text-amber-400'
                      : 'text-muted-foreground hover:text-foreground'
                  )}
                  onClick={onToggleFeed}
                >
                  <Sparkles className='h-4 w-4' />
                </Button>
              </TooltipTrigger>
              <TooltipContent side='bottom'>
                {isFeedOpen
                  ? t('hideRecognitionFeed', '隐藏实时识别流')
                  : t('showRecognitionFeed', '显示实时识别流')}
              </TooltipContent>
            </Tooltip>
          )}
        </div>

        <Separator orientation='vertical' className='mx-0.5 h-5 opacity-60' />

        {/* 全局辅助操作组 */}
        <div className='flex items-center gap-1'>
          {/* 全局静音 */}
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant='outline'
                size='icon'
                className='h-8 w-8 text-muted-foreground shadow-2xs hover:text-foreground'
                onClick={onMuteAll}
              >
                <VolumeX className='h-4 w-4' />
              </Button>
            </TooltipTrigger>
            <TooltipContent side='bottom'>{t('muteAll')}</TooltipContent>
          </Tooltip>

          {/* 出声音量：只在某一路解禁后才有实际效果，因此不做额外置灰 */}
          {onVolumeChange && (
            <Tooltip>
              <TooltipTrigger asChild>
                <div className='flex h-8 w-24 items-center px-1'>
                  <Slider
                    aria-label={t('volume')}
                    min={0}
                    max={100}
                    step={1}
                    value={[Math.round(volume * 100)]}
                    onValueChange={(next) =>
                      onVolumeChange((next[0] ?? 0) / 100)
                    }
                  />
                </div>
              </TooltipTrigger>
              <TooltipContent side='bottom'>
                {t('volume')} {Math.round(volume * 100)}%
              </TooltipContent>
            </Tooltip>
          )}

          {/* 清空所有窗口 */}
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant='outline'
                size='icon'
                className='h-8 w-8 text-muted-foreground shadow-2xs hover:border-destructive/40 hover:bg-destructive/10 hover:text-destructive'
                onClick={onClearAll}
              >
                <Trash2 className='h-4 w-4' />
              </Button>
            </TooltipTrigger>
            <TooltipContent side='bottom'>{t('clearAll')}</TooltipContent>
          </Tooltip>

          {/* 全屏切换 */}
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant='outline'
                size='icon'
                className='h-8 w-8 text-muted-foreground shadow-2xs hover:text-foreground'
                onClick={toggleBrowserFullscreen}
              >
                {isFullscreen ? (
                  <Minimize className='h-4 w-4' />
                ) : (
                  <Maximize className='h-4 w-4' />
                )}
              </Button>
            </TooltipTrigger>
            <TooltipContent side='bottom'>
              {isFullscreen ? t('exitFullscreen') : t('fullscreen')}
            </TooltipContent>
          </Tooltip>
        </div>
      </div>
    </TooltipProvider>
  )
}
