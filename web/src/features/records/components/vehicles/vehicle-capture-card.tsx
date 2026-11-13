import { memo, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { categoryBadgeClass } from '@/lib/category-palette'
import { cn } from '@/lib/utils'
import { Card, CardContent } from '@/components/ui/card'
import { cameraLabel, type CameraNameLookup } from '../../data/camera-label'
import { formatRecordTime } from '../../data/format-time'
import { type Capture } from '../../data/schema'
import { VehicleEvidence } from './vehicle-evidence'

interface VehicleCaptureCardProps {
  item: Capture
  cameraName?: CameraNameLookup
  onSelect: (item: Capture) => void
}

export const VehicleCaptureCard = memo(function VehicleCaptureCard({
  item,
  cameraName,
  onSelect,
}: VehicleCaptureCardProps) {
  const { t } = useTranslation('records')
  const formattedTime = formatRecordTime(item.capturedAt, 'MM-dd HH:mm:ss')
  const score = item.score > 0 ? `${Math.round(item.score * 100)}%` : '-'

  const attrs = item.attributes as Record<string, unknown> | undefined
  const plateNo =
    typeof attrs?.plate_number === 'string' ? attrs.plate_number : ''
  const plateColor =
    typeof attrs?.plate_color === 'string' ? attrs.plate_color : ''

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
      aria-label={t('vehicle.viewDetail')}
      onClick={() => onSelect(item)}
      onKeyDown={handleKeyDown}
      className='group cursor-pointer gap-0 overflow-hidden border-border py-0 transition-colors hover:border-primary/40 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none'
    >
      <VehicleEvidence capture={item} layout='card' />
      <CardContent className='space-y-1.5 p-3'>
        <div className='flex items-center justify-between gap-2 text-xs'>
          <span className='truncate font-medium'>
            {cameraLabel(item.cameraId, cameraName)}
          </span>
          <span className='shrink-0 font-mono text-[11px] text-muted-foreground'>
            {formattedTime}
          </span>
        </div>

        {plateNo && (
          <div className='flex items-center justify-between gap-2'>
            <span
              className={cn(
                'inline-flex items-center rounded border px-1.5 py-0.5 font-mono text-xs font-semibold',
                categoryBadgeClass('blue')
              )}
            >
              {plateNo}
            </span>
            {plateColor && (
              <span className='text-[10px] text-muted-foreground'>
                {plateColor}
              </span>
            )}
          </div>
        )}

        <div className='flex items-center justify-between gap-2 text-[11px] text-muted-foreground'>
          <span className='font-mono font-semibold text-foreground'>
            {score}
          </span>
          {item.trackId > 0 && (
            <span className='font-mono'>#{item.trackId}</span>
          )}
        </div>
      </CardContent>
    </Card>
  )
})
