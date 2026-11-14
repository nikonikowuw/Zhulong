import { Cpu, Video, ZoomIn } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { cameraLabel, type CameraNameLookup } from '../../data/camera-label'
import { algorithmLabel, type RecordItem } from '../../data/schema'
import { MediaImage } from '../common/media-image'

interface RecordDetailCropCardProps {
  path: string
  alt: string
  score: number
  onPreview: () => void
}

export function RecordDetailCropCard({
  path,
  alt,
  score,
  onPreview,
}: RecordDetailCropCardProps) {
  const { t } = useTranslation('records')

  return (
    <div className='space-y-2 rounded-lg border border-border bg-card p-4 shadow-xs'>
      <div className='flex items-center justify-between'>
        <h4 className='text-xs font-semibold tracking-wider text-muted-foreground uppercase'>
          {t('detail.snapshot')}
        </h4>
        {score > 0 && (
          <Badge variant='outline' className='font-mono text-[11px]'>
            {t('detail.confidence')} {Math.round(score * 100)}%
          </Badge>
        )}
      </div>
      <div className='group relative flex h-36 w-full items-center justify-center overflow-hidden rounded-md border border-border bg-muted/40 p-2'>
        <MediaImage
          path={path}
          alt={alt}
          className='size-full'
          imageClassName='object-contain'
        />
        <button
          type='button'
          onClick={onPreview}
          aria-label={t('detail.previewImageLabel', {
            name: t('detail.snapshot'),
          })}
          className='absolute inset-0 flex cursor-zoom-in items-center justify-center bg-black/0 text-white opacity-0 transition-all group-hover:bg-black/25 group-hover:opacity-100 focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-primary focus-visible:outline-hidden'
        >
          <ZoomIn className='size-5 drop-shadow-sm' />
        </button>
      </div>
    </div>
  )
}

export function RecordDetailMetadataStrip({
  record,
  cameraName,
}: {
  record: RecordItem
  cameraName?: CameraNameLookup
}) {
  const { t } = useTranslation('records')

  return (
    <div className='grid grid-cols-2 gap-3 rounded-lg border border-border bg-muted/30 p-3.5 text-xs text-muted-foreground'>
      <div className='space-y-1'>
        <span className='flex items-center gap-1 text-[10px] font-medium uppercase'>
          <Video className='size-3' />
          {t('detail.cameraId')}
        </span>
        <span className='block truncate font-medium text-foreground'>
          {cameraLabel(record.cameraId, cameraName)}
        </span>
      </div>

      {'algorithmId' in record && (
        <div className='space-y-1'>
          <span className='flex items-center gap-1 text-[10px] font-medium uppercase'>
            <Cpu className='size-3' />
            {t('detail.algorithmId')}
          </span>
          <span className='block truncate font-mono text-foreground'>
            {algorithmLabel(record) || '-'}
          </span>
        </div>
      )}

      {'pts' in record && (
        <div className='space-y-1'>
          <span className='block text-[10px] font-medium uppercase'>
            {t('detail.pts')}
          </span>
          <span className='block font-mono text-foreground'>
            {record.pts > 0 ? record.pts : '-'}
          </span>
        </div>
      )}

      {'trackId' in record && (
        <div className='space-y-1'>
          <span className='block text-[10px] font-medium uppercase'>
            {t('card.trackId')}
          </span>
          <span className='block font-mono text-foreground'>
            {record.trackId > 0 ? `#${record.trackId}` : '-'}
          </span>
        </div>
      )}
    </div>
  )
}
