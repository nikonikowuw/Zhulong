import {
  Download,
  ExternalLink,
  Eye,
  EyeOff,
  Layers,
  Maximize2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { evidenceFileName } from '../../data/evidence-file-name'
import {
  isAlarmRecord,
  isCaptureRecord,
  isRecognitionRecord,
  type RecordItem,
} from '../../data/schema'
import { formatTargetType } from '../../data/target-type'
import { BoundingBoxCanvas } from '../common/bounding-box-canvas'
import { getMediaUrl } from '../common/media-utils'

interface RecordDetailScenePanelProps {
  record: RecordItem
  showBoxes: boolean
  onToggleBoxes: () => void
  dimensions: { width: number; height: number } | null
  onDimensionsLoaded: (dims: { width: number; height: number }) => void
  primaryBox?: [number, number, number, number]
  secondaryBox?: [number, number, number, number]
  onOpenFullscreen: () => void
}

export function RecordDetailScenePanel({
  record,
  showBoxes,
  onToggleBoxes,
  dimensions,
  onDimensionsLoaded,
  primaryBox,
  secondaryBox,
  onOpenFullscreen,
}: RecordDetailScenePanelProps) {
  const { t } = useTranslation('records')

  const rec = isRecognitionRecord(record) ? record : null
  const alm = isAlarmRecord(record) ? record : null
  const cap = isCaptureRecord(record) ? record : null

  const primaryLabel = rec
    ? rec.personName || (rec.matched ? t('card.matched') : t('card.stranger'))
    : alm
      ? alm.alarmType
      : cap
        ? formatTargetType(t, cap.targetType)
        : undefined

  return (
    <div className='flex flex-1 flex-col border-b border-border bg-neutral-950 p-4 lg:w-7/12 lg:border-e lg:border-b-0'>
      <div className='mb-2 flex items-center justify-between text-xs'>
        <div className='flex items-center gap-2'>
          <h4 className='flex items-center gap-1.5 font-semibold tracking-wider text-neutral-300 uppercase'>
            <Layers className='size-3.5 text-primary' />
            {t('detail.fullImage')}
          </h4>
          {dimensions && (
            <span className='rounded bg-neutral-800/80 px-1.5 py-0.5 font-mono text-[10px] text-neutral-400'>
              {dimensions.width} × {dimensions.height}
            </span>
          )}
        </div>

        <div className='flex items-center gap-1'>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className='h-7 px-2 text-[11px] text-neutral-400 hover:bg-neutral-800 hover:text-neutral-200'
            onClick={onToggleBoxes}
            title={showBoxes ? t('detail.hideBoxes') : t('detail.showBoxes')}
          >
            {showBoxes ? (
              <Eye className='me-1 size-3.5 text-primary' />
            ) : (
              <EyeOff className='me-1 size-3.5' />
            )}
            {showBoxes ? t('detail.hideBoxes') : t('detail.showBoxes')}
          </Button>
          {record.imagePath && (
            <>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                className='h-7 px-2 text-[11px] text-neutral-400 hover:bg-neutral-800 hover:text-neutral-200'
                onClick={onOpenFullscreen}
                title={t('detail.previewFullscreen')}
                aria-label={t('detail.previewFullscreen')}
              >
                <Maximize2 className='size-3.5' />
              </Button>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                className='h-7 px-2 text-[11px] text-neutral-400 hover:bg-neutral-800 hover:text-neutral-200'
                asChild
              >
                <a
                  href={getMediaUrl(record.imagePath)}
                  target='_blank'
                  rel='noreferrer'
                  title={t('detail.openInNewTab')}
                >
                  <ExternalLink className='size-3.5' />
                </a>
              </Button>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                className='h-7 px-2 text-[11px] text-neutral-400 hover:bg-neutral-800 hover:text-neutral-200'
                asChild
              >
                <a
                  href={getMediaUrl(record.imagePath)}
                  download={evidenceFileName(record.id, record.capturedAt)}
                  title={t('detail.downloadImage')}
                >
                  <Download className='size-3.5' />
                </a>
              </Button>
            </>
          )}
        </div>
      </div>

      <div className='group relative flex flex-1 items-center justify-center overflow-hidden rounded-lg border border-neutral-800 bg-black/90 p-2'>
        <BoundingBoxCanvas
          key={record.imagePath}
          imagePath={record.imagePath}
          alt={t('detail.fullImage')}
          showBoxes={showBoxes}
          onDimensionsLoaded={onDimensionsLoaded}
          primaryBox={primaryBox}
          primaryLabel={primaryLabel}
          secondaryBox={secondaryBox}
          secondaryLabel={secondaryBox ? t('detail.bodyBoxLabel') : undefined}
          onPreviewClick={record.imagePath ? onOpenFullscreen : undefined}
          previewLabel={t('detail.previewImageLabel', {
            name: t('detail.fullImage'),
          })}
          className='max-h-[540px]'
        />
      </div>
      <div className='mt-2 flex items-center justify-between text-[11px] text-neutral-500'>
        <span className='font-mono'>{t('detail.highlightNotice')}</span>
        {record.box && (
          <span className='font-mono text-[10px]'>
            [{record.box.map((n) => n.toFixed(2)).join(', ')}]
          </span>
        )}
      </div>
    </div>
  )
}
