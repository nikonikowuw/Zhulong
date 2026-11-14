import { useEffect, useRef, useState } from 'react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { RecordImagePreviewToolbar } from './record-image-preview-toolbar'

export interface RecordPreviewImageItem {
  id: string
  url: string
  title: string
  subtitle?: string
}

export interface RecordImagePreviewDialogProps {
  images: RecordPreviewImageItem[]
  activeIndex: number | null
  onActiveIndexChange: (index: number | null) => void
}

export function RecordImagePreviewDialog({
  images,
  activeIndex,
  onActiveIndexChange,
}: RecordImagePreviewDialogProps) {
  const { t } = useTranslation('records')
  const containerRef = useRef<HTMLDivElement>(null)

  const current =
    activeIndex !== null && activeIndex >= 0 && activeIndex < images.length
      ? images[activeIndex]
      : null

  const [scale, setScale] = useState(1)
  const [prevKey, setPrevKey] = useState<string | null>(null)
  const [isFullscreen, setIsFullscreen] = useState(false)

  const currentKey = current ? `${activeIndex}:${current.url}` : null
  if (currentKey !== prevKey) {
    setPrevKey(currentKey)
    setScale(1)
  }

  useEffect(() => {
    const handleFullscreenChange = () => {
      setIsFullscreen(Boolean(document.fullscreenElement))
    }
    document.addEventListener('fullscreenchange', handleFullscreenChange)
    return () => {
      document.removeEventListener('fullscreenchange', handleFullscreenChange)
    }
  }, [])

  const hasMultiple = images.length > 1

  const handleClose = () => {
    if (document.fullscreenElement) {
      document.exitFullscreen().catch(() => {})
    }
    onActiveIndexChange(null)
  }

  const goPrev = () => {
    if (!hasMultiple || activeIndex === null) return
    onActiveIndexChange((activeIndex - 1 + images.length) % images.length)
  }

  const goNext = () => {
    if (!hasMultiple || activeIndex === null) return
    onActiveIndexChange((activeIndex + 1) % images.length)
  }

  useEffect(() => {
    if (!current || !hasMultiple || activeIndex === null) return
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'ArrowLeft') {
        e.preventDefault()
        onActiveIndexChange((activeIndex - 1 + images.length) % images.length)
      } else if (e.key === 'ArrowRight') {
        e.preventDefault()
        onActiveIndexChange((activeIndex + 1) % images.length)
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => {
      window.removeEventListener('keydown', handleKeyDown)
    }
  }, [current, hasMultiple, activeIndex, images.length, onActiveIndexChange])

  const toggleFullscreen = async () => {
    try {
      if (!document.fullscreenElement) {
        if (containerRef.current) {
          await containerRef.current.requestFullscreen()
        }
      } else {
        await document.exitFullscreen()
      }
    } catch {
      // 浏览器禁用全屏时容错降级
    }
  }

  const zoomIn = () =>
    setScale((s) => Math.min(3, Math.round((s + 0.25) * 100) / 100))
  const zoomOut = () =>
    setScale((s) => Math.max(0.5, Math.round((s - 0.25) * 100) / 100))
  const resetZoom = () => setScale(1)

  const handleWheel = (e: React.WheelEvent) => {
    e.stopPropagation()
    if (e.deltaY < 0) zoomIn()
    else zoomOut()
  }

  const handleDoubleClick = (e: React.MouseEvent) => {
    e.stopPropagation()
    setScale((s) => (s > 1 ? 1 : 2))
  }

  if (!current) return null

  return (
    <DialogPrimitive.Root
      open={Boolean(current)}
      onOpenChange={(open) => !open && handleClose()}
    >
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className='fixed inset-0 z-[60] bg-black/95 backdrop-blur-md transition-opacity data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:animate-in data-[state=open]:fade-in-0' />

        <DialogPrimitive.Content
          ref={containerRef}
          aria-describedby={undefined}
          className='fixed inset-0 z-[60] flex h-screen w-screen flex-col justify-between overflow-hidden p-0 text-white select-none focus:outline-hidden'
        >
          <DialogPrimitive.Title className='sr-only'>
            {current.title}
          </DialogPrimitive.Title>

          <RecordImagePreviewToolbar
            title={current.title}
            subtitle={current.subtitle}
            activeIndex={activeIndex}
            total={images.length}
            scale={scale}
            isFullscreen={isFullscreen}
            url={current.url}
            onZoomOut={zoomOut}
            onResetZoom={resetZoom}
            onZoomIn={zoomIn}
            onToggleFullscreen={() => void toggleFullscreen()}
            onClose={handleClose}
          />

          <div
            onClick={handleClose}
            onWheel={handleWheel}
            className='relative flex flex-1 cursor-zoom-out items-center justify-center overflow-hidden p-4 sm:p-8'
          >
            {hasMultiple && (
              <Button
                type='button'
                variant='ghost'
                size='icon'
                onClick={(e) => {
                  e.stopPropagation()
                  goPrev()
                }}
                className='absolute start-3 z-20 h-10 w-10 rounded-full border border-white/15 bg-black/50 text-white backdrop-blur-md hover:bg-white/20 hover:text-white sm:start-6'
                title={t('detail.previousImage')}
                aria-label={t('detail.previousImage')}
              >
                <ChevronLeft className='h-5 w-5' />
              </Button>
            )}

            <img
              src={current.url}
              alt={current.title}
              onDoubleClick={handleDoubleClick}
              onClick={(e) => e.stopPropagation()}
              style={{
                transform: `scale(${scale})`,
                transition: 'transform 0.15s ease-out',
              }}
              className='max-h-[75vh] max-w-[90vw] cursor-default rounded-lg object-contain shadow-2xl drop-shadow-[0_15px_35px_rgba(0,0,0,0.85)]'
            />

            {hasMultiple && (
              <Button
                type='button'
                variant='ghost'
                size='icon'
                onClick={(e) => {
                  e.stopPropagation()
                  goNext()
                }}
                className='absolute end-3 z-20 h-10 w-10 rounded-full border border-white/15 bg-black/50 text-white backdrop-blur-md hover:bg-white/20 hover:text-white sm:end-6'
                title={t('detail.nextImage')}
                aria-label={t('detail.nextImage')}
              >
                <ChevronRight className='h-5 w-5' />
              </Button>
            )}
          </div>

          <div
            onClick={(e) => e.stopPropagation()}
            className='z-10 flex flex-col items-center gap-2 bg-gradient-to-t from-black/85 via-black/40 to-transparent px-4 pt-2 pb-4'
          >
            {hasMultiple && (
              <div className='flex max-w-full items-center gap-2 overflow-x-auto rounded-lg border border-white/15 bg-black/60 p-1.5 backdrop-blur-md'>
                {images.map((item, idx) => {
                  const isActive = idx === activeIndex
                  return (
                    <button
                      key={item.id}
                      type='button'
                      onClick={() => onActiveIndexChange(idx)}
                      title={item.title}
                      aria-label={item.title}
                      className={cn(
                        'flex shrink-0 items-center gap-1.5 rounded-md border p-1 transition-all',
                        isActive
                          ? 'border-primary bg-white/15 ring-1 ring-primary'
                          : 'border-white/10 bg-white/5 opacity-70 hover:opacity-100'
                      )}
                    >
                      <img
                        src={item.url}
                        alt={item.title}
                        className='h-9 w-9 rounded object-cover'
                      />
                      <span className='hidden max-w-24 truncate pe-1 text-[11px] font-medium text-white/90 sm:inline'>
                        {item.title}
                      </span>
                    </button>
                  )
                })}
              </div>
            )}
            <p className='rounded-full bg-black/40 px-3 py-0.5 font-mono text-[11px] text-white/60 backdrop-blur-xs'>
              {t('detail.previewShortcutHint')}
            </p>
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}
