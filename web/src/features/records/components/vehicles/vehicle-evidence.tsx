import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { type Capture } from '../../data/schema'
import { MediaImage } from '../common/media-image'

interface VehicleEvidenceProps {
  capture: Capture
  layout: 'card' | 'table'
}

export function VehicleEvidence({ capture, layout }: VehicleEvidenceProps) {
  const { t } = useTranslation('records')
  const isTable = layout === 'table'

  return (
    <div
      className={cn(
        'grid grid-cols-2 gap-2',
        isTable ? 'w-fit' : 'aspect-[3/2]'
      )}
    >
      <div
        className={cn(
          'relative min-w-0 overflow-hidden rounded-md border border-border bg-muted/40',
          isTable && 'h-14 w-24'
        )}
      >
        <MediaImage
          path={capture.imagePath}
          alt={t('vehicle.fullSceneAlt')}
          className='size-full'
          imageClassName='object-contain pb-4'
        />
        <span className='absolute inset-x-0 bottom-0 z-10 truncate bg-background/85 px-1 py-0.5 text-center text-[9px] font-medium text-foreground'>
          {t('vehicle.fullScene')}
        </span>
      </div>
      <div
        className={cn(
          'relative min-w-0 overflow-hidden rounded-md border border-border bg-muted/40',
          isTable && 'h-14 w-24'
        )}
      >
        <MediaImage
          path={capture.cropPath}
          alt={t('vehicle.cropAlt')}
          className='size-full'
          imageClassName='object-contain pb-4'
        />
        <span className='absolute inset-x-0 bottom-0 z-10 truncate bg-background/85 px-1 py-0.5 text-center text-[9px] font-medium text-foreground'>
          {t('vehicle.crop')}
        </span>
      </div>
    </div>
  )
}
