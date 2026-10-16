import { useState } from 'react'
import { cn } from '@/lib/utils'
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
}: BoundingBoxCanvasProps) {
  const [dimensions, setDimensions] = useState({ width: 0, height: 0 })
  const [imgLoaded, setImgLoaded] = useState(false)
  const [imgError, setImgError] = useState(false)

  const resolvedUrl = getMediaUrl(imagePath)

  const hasPrimary = isValidBox(primaryBox)
  const hasSecondary = isValidBox(secondaryBox)

  return (
    <div
      className={cn(
        'relative inline-block overflow-hidden rounded-lg border border-border bg-black/90 shadow-inner',
        className
      )}
    >
      {/* 图像加载占位 */}
      {!imgLoaded && !imgError && (
        <div className='flex aspect-video w-full items-center justify-center bg-muted/40 text-muted-foreground'>
          <span className='text-xs opacity-70'>Loading image...</span>
        </div>
      )}

      {imgError ? (
        <div className='flex aspect-video w-full items-center justify-center bg-muted/50 text-muted-foreground'>
          <span className='text-xs'>Image preview unavailable</span>
        </div>
      ) : (
        <img
          src={resolvedUrl}
          alt={alt}
          className='block max-h-[600px] w-auto max-w-full object-contain'
          onLoad={(e) => {
            const img = e.currentTarget
            setDimensions({
              width: img.naturalWidth,
              height: img.naturalHeight,
            })
            setImgLoaded(true)
          }}
          onError={() => setImgError(true)}
        />
      )}

      {/* 标注覆盖层 (基于百分比自适应吸附) */}
      {imgLoaded && (
        <div className='pointer-events-none absolute inset-0 size-full'>
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
