import { memo, type KeyboardEvent } from 'react'
import { Camera, Eye } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { categoryBadgeClass } from '@/lib/category-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { cameraLabel, type CameraNameLookup } from '../../data/camera-label'
import { formatRecordTime } from '../../data/format-time'
import { type Capture } from '../../data/schema'
import { formatTargetType, targetTypeBadgeClass } from '../../data/target-type'
import { extractVehicleAttributes } from '../../data/vehicle-attributes'
import { MediaImage } from '../common/media-image'

interface CaptureCardProps {
  item: Capture
  cameraName?: CameraNameLookup
  onSelect: (item: Capture) => void
}

export const CaptureCard = memo(function CaptureCard({
  item,
  cameraName,
  onSelect,
}: CaptureCardProps) {
  const { t } = useTranslation('records')
  const formattedTime = formatRecordTime(item.capturedAt, 'MM-dd HH:mm:ss')
  const scorePercent = Math.round((item.score || 0) * 100)
  const { plateNumber } = extractVehicleAttributes(item.attributes)
  const targetLabel = formatTargetType(t, item.targetType)

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      onSelect(item)
    }
  }

  return (
    <Card
      role='button'
      tabIndex={0}
      aria-label={`${targetLabel} - ${t('card.viewDetail')}`}
      onClick={() => onSelect(item)}
      onKeyDown={handleKeyDown}
      className='group relative cursor-pointer overflow-hidden border-border bg-card transition-all hover:border-primary/40 hover:shadow-md focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-hidden'
    >
      <div className='relative aspect-square w-full overflow-hidden bg-muted/40'>
        <MediaImage
          path={item.cropPath || item.imagePath}
          alt={targetLabel}
          className='size-full transition-transform duration-300 group-hover:scale-105'
          imageClassName='object-contain p-2'
          fallbackText={t('card.fallbackCapture')}
        />

        {/* 顶部目标类型 Badge */}
        <div className='absolute start-2 top-2'>
          <Badge
            variant='outline'
            className={cn(
              'flex items-center gap-1 font-medium shadow-sm',
              targetTypeBadgeClass(item.targetType)
            )}
          >
            <Camera className='size-3' />
            {targetLabel}
          </Badge>
        </div>

        {/* 置信度 */}
        {item.score > 0 && (
          <div className='absolute end-2 top-2 rounded bg-black/70 px-1.5 py-0.5 font-mono text-[11px] font-semibold text-white'>
            {scorePercent}%
          </div>
        )}

        <div className='absolute inset-0 flex items-center justify-center bg-black/30 opacity-0 backdrop-blur-[1px] transition-opacity group-hover:opacity-100'>
          <span className='flex items-center gap-1 rounded-full bg-background/90 px-3 py-1 text-xs font-medium text-foreground shadow-sm'>
            <Eye className='size-3.5' />
            {t('card.viewDetail')}
          </span>
        </div>
      </div>

      <CardContent className='space-y-1.5 p-3'>
        <div className='flex items-center justify-between text-xs'>
          <span className='max-w-[120px] truncate font-semibold text-foreground capitalize'>
            {targetLabel}
          </span>
          <span className='font-mono text-[11px] text-muted-foreground'>
            {formattedTime}
          </span>
        </div>

        {plateNumber && (
          <div className='flex items-center justify-between gap-2'>
            <span
              className={cn(
                'inline-flex items-center rounded border px-1.5 py-0.5 font-mono text-xs font-semibold',
                categoryBadgeClass('blue')
              )}
            >
              {plateNumber}
            </span>
          </div>
        )}

        <div className='flex items-center justify-between text-[11px] text-muted-foreground'>
          <span className='max-w-[130px] truncate'>
            {cameraLabel(item.cameraId, cameraName)}
          </span>
          {item.trackId > 0 && (
            <span className='font-mono'>#{item.trackId}</span>
          )}
        </div>
      </CardContent>
    </Card>
  )
})
