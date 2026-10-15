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
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
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
  showAIOverlay?: boolean
  onToggleAIOverlay?: () => void
  isFeedOpen?: boolean
  onToggleFeed?: () => void
}

const LAYOUT_OPTIONS: Array<{
  layout: GridLayout
  icon: typeof Square
  tooltipKey: 'singleView' | 'quadView' | 'nineView' | 'sixteenView'
}> = [
  { layout: 1, icon: Square, tooltipKey: 'singleView' },
  { layout: 4, icon: Grid2X2, tooltipKey: 'quadView' },
  { layout: 9, icon: Grid3X3, tooltipKey: 'nineView' },
  { layout: 16, icon: LayoutGrid, tooltipKey: 'sixteenView' },
]

export function LiveToolbar({
  layout,
  onLayoutChange,
  onMuteAll,
  onClearAll,
  activeStreamsCount,
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
      <div className='flex items-center gap-2'>
        {/* 活跃流状态 Badge */}
        <Badge
          variant='outline'
          className='hidden items-center gap-1.5 border-emerald-500/30 bg-emerald-500/10 px-2.5 py-1 text-xs font-medium text-emerald-600 sm:flex dark:text-emerald-400'
        >
          <span className='h-2 w-2 animate-pulse rounded-full bg-emerald-500' />
          <span>
            {activeStreamsCount} {t('activeStreams')}
          </span>
        </Badge>

        {/* 宫格切换按钮组 */}
        <div className='flex items-center rounded-lg border border-border bg-muted/50 p-1'>
          {LAYOUT_OPTIONS.map((item) => {
            const Icon = item.icon
            return (
              <Tooltip key={item.layout}>
                <TooltipTrigger asChild>
                  <Button
                    variant={layout === item.layout ? 'secondary' : 'ghost'}
                    size='icon'
                    className='h-7 w-7 rounded-md'
                    onClick={() => onLayoutChange(item.layout)}
                  >
                    <Icon className='h-3.5 w-3.5' />
                  </Button>
                </TooltipTrigger>
                <TooltipContent side='bottom'>
                  {t(item.tooltipKey)}
                </TooltipContent>
              </Tooltip>
            )
          })}
        </div>

        <Separator orientation='vertical' className='mx-1 h-5' />

        {/* AI 特性控制组 */}
        {onToggleAIOverlay && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant={showAIOverlay ? 'secondary' : 'outline'}
                size='icon'
                className={`h-8 w-8 transition-colors ${
                  showAIOverlay
                    ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
                    : 'text-muted-foreground'
                }`}
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
                variant={isFeedOpen ? 'secondary' : 'outline'}
                size='icon'
                className={`h-8 w-8 transition-colors ${
                  isFeedOpen
                    ? 'border-primary/40 bg-primary/10 text-primary'
                    : 'text-muted-foreground'
                }`}
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

        {/* 全局辅助操作组 */}
        <div className='flex items-center gap-1'>
          {/* 全局静音 */}
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant='outline'
                size='icon'
                className='h-8 w-8'
                onClick={onMuteAll}
              >
                <VolumeX className='h-4 w-4' />
              </Button>
            </TooltipTrigger>
            <TooltipContent side='bottom'>{t('muteAll')}</TooltipContent>
          </Tooltip>

          {/* 清空所有窗口 */}
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant='outline'
                size='icon'
                className='h-8 w-8 text-muted-foreground hover:border-destructive/40 hover:text-destructive'
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
                className='h-8 w-8'
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
