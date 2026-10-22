import { type KeyboardEvent } from 'react'
import { format } from 'date-fns'
import { useTranslation } from 'react-i18next'
import { Card, CardContent } from '@/components/ui/card'
import { type Capture } from '../../data/schema'
import { VehicleEvidence } from './vehicle-evidence'

interface VehicleCaptureCardProps {
  item: Capture
  onSelect: (item: Capture) => void
}

export function VehicleCaptureCard({
  item,
  onSelect,
}: VehicleCaptureCardProps) {
  const { t } = useTranslation('records')
  const formattedTime = item.capturedAt
    ? format(new Date(item.capturedAt), 'MM-dd HH:mm:ss')
    : '-'
  const score = item.score > 0 ? `${Math.round(item.score * 100)}%` : '-'

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
          <span className='truncate font-medium'>{item.cameraId}</span>
          <span className='shrink-0 font-mono text-[11px] text-muted-foreground'>
            {formattedTime}
          </span>
        </div>
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
}
