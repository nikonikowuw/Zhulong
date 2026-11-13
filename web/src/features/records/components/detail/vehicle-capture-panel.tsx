import { Check, Copy, ExternalLink, ZoomIn } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { type Capture } from '../../data/schema'
import { MediaImage } from '../common/media-image'
import { getMediaUrl } from '../common/media-utils'
import { getPlateBadgeStyle, useCopyFeedback } from './record-detail-helpers'

interface VehicleCapturePanelProps {
  capture: Capture
  onOpenCropPreview: () => void
}

export function VehicleCapturePanel({
  capture: cap,
  onOpenCropPreview,
}: VehicleCapturePanelProps) {
  const { t } = useTranslation('records')
  const plateCopy = useCopyFeedback()

  const attrs = cap.attributes as Record<string, unknown> | undefined
  const plateNo =
    typeof attrs?.plate_number === 'string' ? attrs.plate_number : ''
  const plateColor =
    typeof attrs?.plate_color === 'string' ? attrs.plate_color : ''
  const vehicleType =
    typeof attrs?.vehicle_type === 'string' ? attrs.vehicle_type : ''

  return (
    <div className='space-y-3 rounded-lg border border-border bg-card p-4 shadow-xs'>
      <div className='flex items-center justify-between'>
        <h4 className='text-xs font-semibold tracking-wider text-muted-foreground uppercase'>
          {t('detail.vehicleCrop')}
        </h4>
        {cap.score > 0 && (
          <Badge variant='outline' className='font-mono text-[11px]'>
            {t('detail.confidence')} {Math.round(cap.score * 100)}%
          </Badge>
        )}
      </div>

      {/* 车牌视觉徽标与一键复制 */}
      {plateNo && (
        <div className='flex items-center justify-between gap-2 rounded-md border border-border/80 bg-muted/40 p-2.5'>
          <div className='flex flex-wrap items-center gap-2'>
            <span
              className={cn(
                'inline-flex items-center rounded border px-3 py-1 font-mono text-base font-bold tracking-widest shadow-sm',
                getPlateBadgeStyle(plateColor)
              )}
            >
              {plateNo}
            </span>
            {plateColor && (
              <Badge
                variant='secondary'
                className='text-xs font-medium capitalize'
              >
                {plateColor}
              </Badge>
            )}
            {vehicleType && (
              <Badge
                variant='outline'
                className='text-xs text-muted-foreground capitalize'
              >
                {vehicleType}
              </Badge>
            )}
          </div>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className='h-8 px-2 text-xs text-muted-foreground hover:text-foreground'
            onClick={() => void plateCopy.copy(plateNo)}
          >
            {plateCopy.copied ? (
              <Check className='me-1 size-3.5 text-emerald-500' />
            ) : (
              <Copy className='me-1 size-3.5' />
            )}
            {plateCopy.copied ? t('detail.copied') : t('detail.copyPlate')}
          </Button>
        </div>
      )}

      {/* 车辆切片高清展示 */}
      {cap.cropPath && (
        <div className='group relative flex h-36 w-full items-center justify-center overflow-hidden rounded-md border border-border bg-muted/30 p-2'>
          <MediaImage
            path={cap.cropPath}
            alt={t('vehicle.cropAlt')}
            className='size-full'
            imageClassName='object-contain'
          />
          <button
            type='button'
            onClick={onOpenCropPreview}
            aria-label={t('detail.previewImageLabel', {
              name: t('detail.vehicleCrop'),
            })}
            className='absolute inset-0 cursor-zoom-in bg-transparent focus-visible:ring-2 focus-visible:ring-primary focus-visible:outline-hidden'
          />
          <div className='absolute end-2 top-2 z-10 flex items-center gap-1 opacity-0 transition-opacity group-hover:opacity-100'>
            <Button
              type='button'
              variant='secondary'
              size='sm'
              className='size-7 p-0 shadow-sm'
              onClick={onOpenCropPreview}
              title={t('detail.previewFullscreen')}
              aria-label={t('detail.previewFullscreen')}
            >
              <ZoomIn className='size-3.5' />
            </Button>
            <Button
              type='button'
              variant='secondary'
              size='sm'
              className='size-7 p-0 shadow-sm'
              asChild
            >
              <a
                href={getMediaUrl(cap.cropPath)}
                target='_blank'
                rel='noreferrer'
                title={t('detail.openInNewTab')}
              >
                <ExternalLink className='size-3.5' />
              </a>
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}
