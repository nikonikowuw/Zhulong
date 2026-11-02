import { useState } from 'react'
import { ImageOff, Loader2, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { type BoundingBox } from '../../data/schema'
import { getMediaUrl } from './media-utils'

export interface BoundingBoxItem {
  box: BoundingBox
  label?: string
  color?: 'emerald' | 'amber' | 'sky' | 'rose' | 'violet'
  variant?: 'solid' | 'dashed'
}

export interface BoundingBoxCanvasProps {
  imagePath: string
  alt?: string
  primaryBox?: BoundingBox
  primaryLabel?: string
  secondaryBox?: BoundingBox
  secondaryLabel?: string
  className?: string
  imageClassName?: string
  /** 控制是否渲染标注覆盖层，默认 true */
  showBoxes?: boolean
  /** 图像尺寸加载成功后的回调 */
  onDimensionsLoaded?: (dimensions: { width: number; height: number }) => void
}

function isValidBox(box?: BoundingBox): boolean {
  if (!box || box.length !== 4) return false
  const [x1, y1, x2, y2] = box
  return x2 > x1 && y2 > y1
}

function normalizeCoords(
  box: BoundingBox,
  naturalWidth: number,
  naturalHeight: number
): { x: number; y: number; width: number; height: number } {
  const [x1, y1, x2, y2] = box
  const isNormalized = x2 <= 1.05 && y2 <= 1.05

  if (isNormalized) {
    return {
      x: Math.max(0, x1 * 100),
      y: Math.max(0, y1 * 100),
      width: Math.min(100, (x2 - x1) * 100),
      height: Math.min(100, (y2 - y1) * 100),
    }
  }

  const nw = naturalWidth > 0 ? naturalWidth : 1920
  const nh = naturalHeight > 0 ? naturalHeight : 1080

  return {
    x: Math.max(0, (x1 / nw) * 100),
    y: Math.max(0, (y1 / nh) * 100),
    width: Math.min(100, ((x2 - x1) / nw) * 100),
    height: Math.min(100, ((y2 - y1) / nh) * 100),
  }
}

export function BoundingBoxCanvas({
  imagePath,
  alt = 'Inspection Scene',
  primaryBox,
  primaryLabel,
  secondaryBox,
  secondaryLabel,
  className,
  imageClassName,
  showBoxes = true,
  onDimensionsLoaded,
}: BoundingBoxCanvasProps) {
  const { t } = useTranslation('records')
  const [dimensions, setDimensions] = useState({ width: 0, height: 0 })
  const [imgLoaded, setImgLoaded] = useState(false)
  const [imgError, setImgError] = useState(false)
  const [retryNonce, setRetryNonce] = useState(0)

  // 当 imagePath 变动时在 render 阶段同步重置状态，符合 React 最佳实践且避免闪烁
  const [prevImagePath, setPrevImagePath] = useState(imagePath)
  if (imagePath !== prevImagePath) {
    setPrevImagePath(imagePath)
    setImgLoaded(false)
    setImgError(false)
    setDimensions({ width: 0, height: 0 })
  }

  const resolvedUrl = getMediaUrl(imagePath)

  const hasPrimary = isValidBox(primaryBox)
  const hasSecondary = isValidBox(secondaryBox)

  return (
    <div
      className={cn(
        'relative inline-flex items-center justify-center overflow-hidden rounded-md bg-black/90',
        // 加载未完成且无错误时，保持 16:9 比例自适应占位，平滑不突变
        !imgLoaded &&
          !imgError &&
          'aspect-video min-h-[260px] w-full max-w-full'
      )}
    >
      {/* 图像加载占位：绝对定位覆盖，平滑淡出，半透明纯黑背景与视口浑然一体 */}
      {!imgError && (
        <div
          className={cn(
            'pointer-events-none absolute inset-0 z-10 flex flex-col items-center justify-center gap-3 bg-black/75 p-6 backdrop-blur-xs transition-opacity duration-300 select-none',
            imgLoaded ? 'opacity-0' : 'opacity-100'
          )}
          aria-hidden={imgLoaded}
        >
          <div className='relative flex items-center justify-center'>
            <div className='size-12 animate-ping rounded-full bg-primary/10' />
            <div className='absolute size-8 rounded-full border border-primary/20 bg-primary/5' />
            <Loader2 className='size-6 animate-spin text-primary' />
          </div>
          <div className='flex flex-col items-center gap-1 text-center'>
            <span className='text-xs font-medium tracking-wide text-neutral-300'>
              {t('detail.loadingImage', {
                defaultValue: '正在加载全景图像...',
              })}
            </span>
            <span className='font-mono text-[10px] text-neutral-500'>
              {t('detail.loadingImageHint', {
                defaultValue: '正在传输原始画面帧',
              })}
            </span>
          </div>
        </div>
      )}

      {/* 错误提示态 */}
      {imgError ? (
        <div className='flex aspect-video w-full max-w-2xl min-w-[280px] flex-col items-center justify-center gap-3 bg-neutral-900/60 p-6 text-neutral-400 select-none'>
          <div className='flex size-12 items-center justify-center rounded-full bg-neutral-800/80 text-destructive/80'>
            <ImageOff className='size-6' />
          </div>
          <div className='flex flex-col items-center gap-1 text-center'>
            <span className='text-xs font-medium text-neutral-300'>
              {t('detail.imageLoadError', {
                defaultValue: '全景图像加载失败',
              })}
            </span>
            <span className='font-mono text-[10px] text-neutral-500'>
              {t('detail.imageLoadErrorHint', {
                defaultValue: '请检查网络连接或源文件',
              })}
            </span>
          </div>
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='mt-1 h-7 border-neutral-700 bg-neutral-800/60 px-3 text-xs text-neutral-300 hover:bg-neutral-800 hover:text-white'
            onClick={() => {
              setImgError(false)
              setImgLoaded(false)
              setRetryNonce((k) => k + 1)
            }}
          >
            <RefreshCw className='me-1.5 size-3' />
            {t('detail.retry', { defaultValue: '重试' })}
          </Button>
        </div>
      ) : (
        <img
          key={`${resolvedUrl}-${retryNonce}`}
          src={resolvedUrl}
          alt={alt}
          className={cn(
            'block w-auto max-w-full transition-opacity duration-300 ease-out',
            className || 'max-h-[600px]',
            imgLoaded ? 'opacity-100' : 'opacity-0',
            imageClassName
          )}
          onLoad={(e) => {
            const img = e.currentTarget
            const dims = {
              width: img.naturalWidth,
              height: img.naturalHeight,
            }
            setDimensions(dims)
            setImgLoaded(true)
            onDimensionsLoaded?.(dims)
          }}
          onError={() => setImgError(true)}
        />
      )}

      {/* 标注覆盖层 (平滑淡入，基于百分比自适应吸附) */}
      {imgLoaded && !imgError && showBoxes && (
        <div className='pointer-events-none absolute inset-0 size-full animate-in duration-300 fade-in'>
          {/* 次要标注框（如人体外框） */}
          {hasSecondary &&
            secondaryBox &&
            (() => {
              const rect = normalizeCoords(
                secondaryBox,
                dimensions.width,
                dimensions.height
              )
              return (
                <div
                  className='absolute border-2 border-dashed border-violet-400/80 bg-violet-500/10 transition-all'
                  style={{
                    left: `${rect.x}%`,
                    top: `${rect.y}%`,
                    width: `${rect.width}%`,
                    height: `${rect.height}%`,
                  }}
                >
                  {secondaryLabel && (
                    <div className='absolute -top-5 left-0 rounded bg-violet-600/90 px-1.5 py-0.5 text-[10px] font-semibold whitespace-nowrap text-white shadow-sm'>
                      {secondaryLabel}
                    </div>
                  )}
                </div>
              )
            })()}

          {/* 主要标注框（如人脸框、告警目标框、抓拍物框） */}
          {hasPrimary &&
            primaryBox &&
            (() => {
              const rect = normalizeCoords(
                primaryBox,
                dimensions.width,
                dimensions.height
              )
              return (
                <div
                  className='absolute border-2 border-emerald-400 bg-emerald-500/15 shadow-[0_0_12px_rgba(16,185,129,0.4)] transition-all'
                  style={{
                    left: `${rect.x}%`,
                    top: `${rect.y}%`,
                    width: `${rect.width}%`,
                    height: `${rect.height}%`,
                  }}
                >
                  {primaryLabel && (
                    <div className='absolute -top-6 left-0 flex items-center gap-1 rounded bg-emerald-600 px-1.5 py-0.5 text-[11px] font-medium whitespace-nowrap text-white shadow-md'>
                      <span className='size-1.5 animate-pulse rounded-full bg-white' />
                      {primaryLabel}
                    </div>
                  )}
                </div>
              )
            })()}
        </div>
      )}
    </div>
  )
}
