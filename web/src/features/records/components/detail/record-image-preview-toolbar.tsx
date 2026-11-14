import * as DialogPrimitive from '@radix-ui/react-dialog'
import {
  ExternalLink,
  Maximize2,
  Minimize2,
  RotateCcw,
  X,
  ZoomIn,
  ZoomOut,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'

interface RecordImagePreviewToolbarProps {
  title: string
  subtitle?: string
  activeIndex: number | null
  total: number
  scale: number
  isFullscreen: boolean
  url: string
  onZoomOut: () => void
  onResetZoom: () => void
  onZoomIn: () => void
  onToggleFullscreen: () => void
  onClose: () => void
}

export function RecordImagePreviewToolbar({
  title,
  subtitle,
  activeIndex,
  total,
  scale,
  isFullscreen,
  url,
  onZoomOut,
  onResetZoom,
  onZoomIn,
  onToggleFullscreen,
  onClose,
}: RecordImagePreviewToolbarProps) {
  const { t } = useTranslation('records')
  const hasMultiple = total > 1

  return (
    <div className='z-10 flex items-center justify-between gap-3 bg-gradient-to-b from-black/85 via-black/50 to-transparent p-4 sm:px-6'>
      <div className='flex min-w-0 items-center gap-2'>
        <span className='truncate text-xs font-semibold text-white/95 drop-shadow-xs sm:text-sm'>
          {title}
        </span>
        {subtitle && (
          <span className='hidden truncate rounded-full bg-white/10 px-2 py-0.5 font-mono text-[11px] text-white/80 sm:inline-block'>
            {subtitle}
          </span>
        )}
        {hasMultiple && activeIndex !== null && (
          <span className='shrink-0 rounded-full bg-white/10 px-2 py-0.5 font-mono text-[10px] text-white/75'>
            {activeIndex + 1} / {total}
          </span>
        )}
        <span className='shrink-0 rounded-full bg-white/10 px-2 py-0.5 font-mono text-[10px] text-white/75'>
          {Math.round(scale * 100)}%
        </span>
      </div>

      <div className='flex shrink-0 items-center gap-1 rounded-full border border-white/15 bg-white/10 p-1 backdrop-blur-md sm:gap-1.5'>
        <Button
          type='button'
          variant='ghost'
          size='icon'
          onClick={onZoomOut}
          disabled={scale <= 0.5}
          className='h-8 w-8 text-white hover:bg-white/20 hover:text-white disabled:opacity-40'
          title={t('detail.zoomOut')}
          aria-label={t('detail.zoomOut')}
        >
          <ZoomOut className='h-4 w-4' />
        </Button>
        <Button
          type='button'
          variant='ghost'
          size='icon'
          onClick={onResetZoom}
          className='h-8 w-8 text-white hover:bg-white/20 hover:text-white'
          title={t('detail.zoomReset')}
          aria-label={t('detail.zoomReset')}
        >
          <RotateCcw className='h-3.5 w-3.5' />
        </Button>
        <Button
          type='button'
          variant='ghost'
          size='icon'
          onClick={onZoomIn}
          disabled={scale >= 3}
          className='h-8 w-8 text-white hover:bg-white/20 hover:text-white disabled:opacity-40'
          title={t('detail.zoomIn')}
          aria-label={t('detail.zoomIn')}
        >
          <ZoomIn className='h-4 w-4' />
        </Button>

        <div className='h-4 w-px bg-white/20' />

        <Button
          type='button'
          variant='ghost'
          size='icon'
          onClick={onToggleFullscreen}
          className='h-8 w-8 text-white hover:bg-white/20 hover:text-white'
          title={t(
            isFullscreen ? 'detail.exitFullscreen' : 'detail.enterFullscreen'
          )}
          aria-label={t(
            isFullscreen ? 'detail.exitFullscreen' : 'detail.enterFullscreen'
          )}
        >
          {isFullscreen ? (
            <Minimize2 className='h-4 w-4' />
          ) : (
            <Maximize2 className='h-4 w-4' />
          )}
        </Button>

        <Button
          type='button'
          variant='ghost'
          size='icon'
          asChild
          className='h-8 w-8 text-white hover:bg-white/20 hover:text-white'
          title={t('detail.openInNewTab')}
          aria-label={t('detail.openInNewTab')}
        >
          <a href={url} target='_blank' rel='noopener noreferrer'>
            <ExternalLink className='h-4 w-4' />
          </a>
        </Button>

        <div className='h-4 w-px bg-white/20' />

        <DialogPrimitive.Close asChild>
          <Button
            type='button'
            variant='ghost'
            size='icon'
            onClick={onClose}
            className='hover:text-destructive-foreground h-8 w-8 text-white hover:bg-destructive'
            title={t('detail.close')}
            aria-label={t('detail.close')}
          >
            <X className='h-4 w-4' />
          </Button>
        </DialogPrimitive.Close>
      </div>
    </div>
  )
}
