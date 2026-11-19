import { useEffect, useRef, useState } from 'react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { type BoundingBox, type LandmarkPoint } from '../../data/schema'
import { RecordImagePreviewToolbar } from './record-image-preview-toolbar'

/** 全屏预览里的一张图要叠加的标注框；与详情页内嵌画布保持同一套几何语义。 */
export interface RecordPreviewBox {
  box: BoundingBox
  label?: string
  variant?: 'primary' | 'secondary'
}

export interface RecordPreviewImageItem {
  id: string
  url: string
  title: string
  subtitle?: string
  landmarks?: LandmarkPoint[]
  boxes?: RecordPreviewBox[]
}

export interface RecordImagePreviewDialogProps {
  images: RecordPreviewImageItem[]
  activeIndex: number | null
  onActiveIndexChange: (index: number | null) => void
  /** 与详情页“隐藏标注框”开关联动；关闭时框和关键点一起隐藏。 */
  showBoxes?: boolean
}

/** 把记录里的框投影成百分比矩形；兼容历史数据里的像素坐标。 */
function projectPreviewBox(
  box: BoundingBox,
  naturalWidth: number,
  naturalHeight: number
): { x: number; y: number; width: number; height: number } | null {
  if (!box || box.length !== 4) return null
  const [x1, y1, x2, y2] = box
  if (![x1, y1, x2, y2].every(Number.isFinite)) return null

  const isNormalized = x2 <= 1.05 && y2 <= 1.05
  const nw = naturalWidth > 0 ? naturalWidth : 1920
  const nh = naturalHeight > 0 ? naturalHeight : 1080

  const left = Math.max(0, Math.min(1, isNormalized ? x1 : x1 / nw))
  const top = Math.max(0, Math.min(1, isNormalized ? y1 : y1 / nh))
  const right = Math.max(0, Math.min(1, isNormalized ? x2 : x2 / nw))
  const bottom = Math.max(0, Math.min(1, isNormalized ? y2 : y2 / nh))
  if (right <= left || bottom <= top) return null

  return {
    x: left * 100,
    y: top * 100,
    width: (right - left) * 100,
    height: (bottom - top) * 100,
  }
}

export function RecordImagePreviewDialog({
  images,
  activeIndex,
  onActiveIndexChange,
  showBoxes = true,
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
  const [naturalSize, setNaturalSize] = useState({ width: 0, height: 0 })

  const currentKey = current ? `${activeIndex}:${current.url}` : null
  if (currentKey !== prevKey) {
    setPrevKey(currentKey)
    setScale(1)
    setNaturalSize({ width: 0, height: 0 })
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

            <div
              onDoubleClick={handleDoubleClick}
              onClick={(e) => e.stopPropagation()}
              style={{
                transform: `scale(${scale})`,
                transition: 'transform 0.15s ease-out',
              }}
              className='relative inline-flex cursor-default items-center justify-center'
            >
              <img
                src={current.url}
                alt={current.title}
                onLoad={(e) =>
                  setNaturalSize({
                    width: e.currentTarget.naturalWidth,
                    height: e.currentTarget.naturalHeight,
                  })
                }
                className='max-h-[75vh] max-w-[90vw] rounded-lg object-contain shadow-2xl drop-shadow-[0_15px_35px_rgba(0,0,0,0.85)]'
              />
              {showBoxes &&
                ((current.boxes && current.boxes.length > 0) ||
                  (current.landmarks && current.landmarks.length > 0)) && (
                  <div className='pointer-events-none absolute inset-0'>
                    {current.boxes?.map((item, index) => {
                      const rect = projectPreviewBox(
                        item.box,
                        naturalSize.width,
                        naturalSize.height
                      )
                      if (!rect) return null
                      const secondary = item.variant === 'secondary'
                      return (
                        <div
                          key={`preview-box-${index}`}
                          data-testid={`preview-box-${
                            secondary ? 'secondary' : 'primary'
                          }`}
                          className={cn(
                            'absolute border-2',
                            secondary
                              ? 'border-dashed border-violet-400/80 bg-violet-500/10'
                              : 'border-emerald-400 bg-emerald-500/15'
                          )}
                          style={{
                            left: `${rect.x}%`,
                            top: `${rect.y}%`,
                            width: `${rect.width}%`,
                            height: `${rect.height}%`,
                          }}
                        >
                          {item.label && (
                            <span
                              className={cn(
                                'absolute start-0 -top-5 rounded px-1.5 py-0.5 text-[10px] font-semibold whitespace-nowrap text-white shadow-sm',
                                secondary
                                  ? 'bg-violet-600/90'
                                  : 'bg-emerald-600'
                              )}
                            >
                              {item.label}
                            </span>
                          )}
                        </div>
                      )
                    })}
                    {current.landmarks?.map(([lx, ly], index) =>
                      Number.isFinite(lx) &&
                      Number.isFinite(ly) &&
                      lx >= 0 &&
                      lx <= 1 &&
                      ly >= 0 &&
                      ly <= 1 ? (
                        <div
                          key={`preview-landmark-${index}`}
                          data-testid='preview-landmark-point'
                          className='absolute size-1 -translate-x-1/2 -translate-y-1/2 rounded-full bg-amber-300 ring-1 ring-black/80'
                          style={{
                            left: `${lx * 100}%`,
                            top: `${ly * 100}%`,
                          }}
                        />
                      ) : null
                    )}
                  </div>
                )}
            </div>

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
