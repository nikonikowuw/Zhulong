import { useState, type ImgHTMLAttributes } from 'react'
import { ImageOff } from 'lucide-react'
import { cn } from '@/lib/utils'
import { getMediaUrl } from './media-utils'

export interface MediaImageProps extends ImgHTMLAttributes<HTMLImageElement> {
  path?: string
  fallbackText?: string
  showLoadingSkeleton?: boolean
}

export function MediaImage({
  path,
  src,
  alt = 'Media',
  className,
  fallbackText,
  showLoadingSkeleton = true,
  ...props
}: MediaImageProps) {
  const [error, setError] = useState(false)
  const [loaded, setLoaded] = useState(false)

  const resolvedUrl = path ? getMediaUrl(path) : (src ?? '')

  if (!resolvedUrl || error) {
    return (
      <div
        className={cn(
          'flex flex-col items-center justify-center bg-muted/60 text-muted-foreground select-none',
          className
        )}
        aria-label={alt}
      >
        <ImageOff className='size-5 opacity-60' />
        {fallbackText ? (
          <span className='mt-1 text-[11px] font-medium tracking-tight opacity-75'>
            {fallbackText}
          </span>
        ) : null}
      </div>
    )
  }

  return (
    <div className={cn('relative overflow-hidden bg-muted/30', className)}>
      {!loaded && showLoadingSkeleton && (
        <div className='absolute inset-0 animate-pulse bg-muted/70' />
      )}
      <img
        src={resolvedUrl}
        alt={alt}
        loading='lazy'
        decoding='async'
        onLoad={() => setLoaded(true)}
        onError={() => setError(true)}
        className={cn(
          'size-full object-cover transition-opacity duration-300',
          loaded ? 'opacity-100' : 'opacity-0'
        )}
        {...props}
      />
    </div>
  )
}
