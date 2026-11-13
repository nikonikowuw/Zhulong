import { useMemo, useState } from 'react'
import {
  AlertTriangle,
  Camera,
  Check,
  Copy,
  Cpu,
  UserCheck,
  UserX,
  Video,
  ZoomIn,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { boxesCoincide } from '@/lib/box-geometry'
import { statusDotClass, statusLevelClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { ScrollArea } from '@/components/ui/scroll-area'
import { cameraLabel, type CameraNameLookup } from '../../data/camera-label'
import { formatRecordTime } from '../../data/format-time'
import {
  algorithmLabel,
  isAlarmRecord,
  isCaptureRecord,
  isRecognitionRecord,
  type RecordItem,
  type RecordsTab,
} from '../../data/schema'
import { formatTargetType, targetTypeBadgeClass } from '../../data/target-type'
import { MediaImage } from '../common/media-image'
import { RecognitionComparisonPanel } from './recognition-comparison-panel'
import { buildPreviewImages, useCopyFeedback } from './record-detail-helpers'
import { RecordDetailScenePanel } from './record-detail-scene-panel'
import { RecordImagePreviewDialog } from './record-image-preview-dialog'
import { RecordJsonViewer } from './record-json-viewer'
import { VehicleCapturePanel } from './vehicle-capture-panel'

export interface RecordDetailDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  record: RecordItem | null
  tab: RecordsTab
  /** `cameraId → 名称` 解析器，见 data/camera-label.ts */
  cameraName?: CameraNameLookup
}

export function RecordDetailDialog({
  open,
  onOpenChange,
  record,
  tab,
  cameraName,
}: RecordDetailDialogProps) {
  const { t } = useTranslation('records')
  const idCopy = useCopyFeedback()
  const [showBoxes, setShowBoxes] = useState(true)
  const [selectedCandidateIdx, setSelectedCandidateIdx] = useState<
    number | null
  >(null)
  const [previewIndex, setPreviewIndex] = useState<number | null>(null)
  const [dimensions, setDimensions] = useState<{
    width: number
    height: number
  } | null>(null)

  // 切换记录或关闭弹窗时在 render 阶段同步重置状态，防止重新打开同记录时直接弹出全屏预览
  const [prevRecordId, setPrevRecordId] = useState(record?.id)
  const [prevOpen, setPrevOpen] = useState(open)
  if (record?.id !== prevRecordId || (prevOpen && !open)) {
    setPrevRecordId(record?.id)
    setPrevOpen(open)
    setDimensions(null)
    setSelectedCandidateIdx(null)
    setPreviewIndex(null)
  } else if (open !== prevOpen) {
    setPrevOpen(open)
  }

  const previewImages = useMemo(
    () => (record && open ? buildPreviewImages(record, tab, t) : []),
    [record, open, tab, t]
  )

  if (!record) return null

  const rec = isRecognitionRecord(record) ? record : null
  const alm = isAlarmRecord(record) ? record : null
  const cap = isCaptureRecord(record) ? record : null

  const isRecognition = rec !== null
  const isAlarm = alm !== null
  const isVehicleCapture = cap !== null && tab === 'vehicles'
  const isCapture = cap !== null

  const openPreviewById = (id: string) => {
    const idx = previewImages.findIndex((item) => item.id === id)
    if (idx !== -1) {
      setPreviewIndex(idx)
    }
  }

  const recognitionBodyBox =
    isRecognition && rec && !boxesCoincide(rec.bodyBox, rec.box)
      ? rec.bodyBox
      : undefined

  const capturedTime = formatRecordTime(record.capturedAt)
  const dialogTitle = isRecognition
    ? t('detail.recognitionTitle')
    : isAlarm
      ? t('detail.alarmTitle')
      : isVehicleCapture
        ? t('detail.vehicleTitle')
        : t('detail.captureTitle')

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className='flex max-h-[92vh] w-[96vw] flex-col overflow-hidden p-0 sm:max-h-[88vh] sm:max-w-4xl lg:max-w-5xl xl:max-w-6xl'>
          <DialogHeader className='border-b border-border px-6 py-4'>
            <div className='flex items-center justify-between gap-4'>
              <div className='space-y-1.5'>
                <DialogTitle className='flex flex-wrap items-center gap-2.5 text-lg font-bold'>
                  <span>{dialogTitle}</span>
                  {isRecognition && rec && (
                    <Badge
                      variant='outline'
                      className={cn(
                        'gap-1.5 font-medium',
                        statusLevelClass(rec.matched ? 'active' : 'warning')
                      )}
                    >
                      <span
                        className={cn(
                          'size-1.5 rounded-full',
                          statusDotClass(rec.matched ? 'active' : 'warning')
                        )}
                      />
                      {rec.matched ? (
                        <UserCheck className='size-3' />
                      ) : (
                        <UserX className='size-3' />
                      )}
                      {rec.matched
                        ? rec.personName || t('card.matched')
                        : t('card.stranger')}
                    </Badge>
                  )}
                  {isAlarm && alm && (
                    <Badge
                      variant='outline'
                      className={cn(
                        'gap-1.5 font-medium',
                        statusLevelClass('destructive')
                      )}
                    >
                      <span
                        className={cn(
                          'size-1.5 rounded-full',
                          statusDotClass('destructive')
                        )}
                      />
                      <AlertTriangle className='size-3' />
                      {alm.alarmType}
                    </Badge>
                  )}
                  {isCapture && cap && (
                    <Badge
                      variant='outline'
                      className={cn(
                        'gap-1.5 font-medium capitalize',
                        targetTypeBadgeClass(cap.targetType)
                      )}
                    >
                      <Camera className='size-3' />
                      {formatTargetType(t, cap.targetType)}
                    </Badge>
                  )}
                </DialogTitle>
                <DialogDescription className='flex flex-wrap items-center gap-3 text-xs text-muted-foreground'>
                  <span>
                    {t('detail.cameraId')}:{' '}
                    <strong className='text-foreground'>
                      {cameraLabel(record.cameraId, cameraName)}
                    </strong>
                  </span>
                  <span>•</span>
                  <span>
                    {t('detail.capturedAt')}:{' '}
                    <strong className='font-mono text-foreground'>
                      {capturedTime}
                    </strong>
                  </span>
                </DialogDescription>
              </div>
            </div>
          </DialogHeader>

          <div className='flex flex-1 flex-col overflow-hidden lg:flex-row'>
            <RecordDetailScenePanel
              record={record}
              showBoxes={showBoxes}
              onToggleBoxes={() => setShowBoxes((v) => !v)}
              dimensions={dimensions}
              onDimensionsLoaded={setDimensions}
              primaryBox={record.box}
              secondaryBox={recognitionBodyBox}
              onOpenFullscreen={() => openPreviewById('full')}
            />

            <ScrollArea className='flex-1 lg:w-5/12'>
              <div className='space-y-4 p-5'>
                {isVehicleCapture && cap && (
                  <VehicleCapturePanel
                    capture={cap}
                    onOpenCropPreview={() => openPreviewById('capture-crop')}
                  />
                )}

                {isRecognition && rec && (
                  <RecognitionComparisonPanel
                    record={rec}
                    selectedCandidateIdx={selectedCandidateIdx}
                    onSelectCandidateIdx={setSelectedCandidateIdx}
                    previewImages={previewImages}
                    onOpenPreviewById={openPreviewById}
                    onOpenPreviewIndex={setPreviewIndex}
                  />
                )}

                {isAlarm && alm && alm.cropPath && (
                  <div className='space-y-2 rounded-lg border border-border bg-card p-4 shadow-xs'>
                    <div className='flex items-center justify-between'>
                      <h4 className='text-xs font-semibold tracking-wider text-muted-foreground uppercase'>
                        {t('detail.snapshot')}
                      </h4>
                      {alm.score > 0 && (
                        <Badge
                          variant='outline'
                          className='font-mono text-[11px]'
                        >
                          {t('detail.confidence')} {Math.round(alm.score * 100)}
                          %
                        </Badge>
                      )}
                    </div>
                    <div className='group relative flex h-36 w-full items-center justify-center overflow-hidden rounded-md border border-border bg-muted/40 p-2'>
                      <MediaImage
                        path={alm.cropPath}
                        alt={alm.alarmType}
                        className='size-full'
                        imageClassName='object-contain'
                      />
                      <button
                        type='button'
                        onClick={() => openPreviewById('alarm-crop')}
                        aria-label={t('detail.previewImageLabel', {
                          name: t('detail.snapshot'),
                        })}
                        className='absolute inset-0 flex cursor-zoom-in items-center justify-center bg-black/0 text-white opacity-0 transition-all group-hover:bg-black/25 group-hover:opacity-100 focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-primary focus-visible:outline-hidden'
                      >
                        <ZoomIn className='size-5 drop-shadow-sm' />
                      </button>
                    </div>
                  </div>
                )}

                {isCapture && !isVehicleCapture && cap && cap.cropPath && (
                  <div className='space-y-2 rounded-lg border border-border bg-card p-4 shadow-xs'>
                    <div className='flex items-center justify-between'>
                      <h4 className='text-xs font-semibold tracking-wider text-muted-foreground uppercase'>
                        {t('detail.snapshot')}
                      </h4>
                      {cap.score > 0 && (
                        <Badge
                          variant='outline'
                          className='font-mono text-[11px]'
                        >
                          {t('detail.confidence')} {Math.round(cap.score * 100)}
                          %
                        </Badge>
                      )}
                    </div>
                    <div className='group relative flex h-36 w-full items-center justify-center overflow-hidden rounded-md border border-border bg-muted/40 p-2'>
                      <MediaImage
                        path={cap.cropPath}
                        alt={formatTargetType(t, cap.targetType)}
                        className='size-full'
                        imageClassName='object-contain'
                      />
                      <button
                        type='button'
                        onClick={() => openPreviewById('capture-crop')}
                        aria-label={t('detail.previewImageLabel', {
                          name: t('detail.snapshot'),
                        })}
                        className='absolute inset-0 flex cursor-zoom-in items-center justify-center bg-black/0 text-white opacity-0 transition-all group-hover:bg-black/25 group-hover:opacity-100 focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-primary focus-visible:outline-hidden'
                      >
                        <ZoomIn className='size-5 drop-shadow-sm' />
                      </button>
                    </div>
                  </div>
                )}

                <div className='space-y-2 rounded-lg border border-border bg-card p-4 shadow-xs'>
                  <h4 className='text-xs font-semibold tracking-wider text-muted-foreground uppercase'>
                    {isAlarm ? t('detail.extra') : t('detail.attributes')}
                  </h4>
                  <RecordJsonViewer
                    data={
                      (isAlarm && alm
                        ? (alm.extra as Record<string, unknown>)
                        : isRecognition && rec
                          ? (rec.attributes as Record<string, unknown>)
                          : isCapture && cap
                            ? (cap.attributes as Record<string, unknown>)
                            : {}) ?? {}
                    }
                  />
                </div>

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
              </div>
            </ScrollArea>
          </div>

          <DialogFooter className='flex items-center justify-between border-t border-border bg-muted/10 px-6 py-3'>
            <div className='flex items-center gap-1 text-xs text-muted-foreground'>
              <span className='font-mono text-[11px]'>
                {t('detail.recordId')}:
              </span>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                className='h-6 px-1 font-mono text-[11px] text-muted-foreground hover:text-foreground'
                onClick={() => void idCopy.copy(record.id)}
              >
                {record.id}
                {idCopy.copied ? (
                  <Check className='ms-1 size-3 text-emerald-500' />
                ) : (
                  <Copy className='ms-1 size-3' />
                )}
              </Button>
            </div>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() => onOpenChange(false)}
            >
              {t('detail.close')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <RecordImagePreviewDialog
        images={previewImages}
        activeIndex={open ? previewIndex : null}
        onActiveIndexChange={setPreviewIndex}
      />
    </>
  )
}
