import { useMemo, useState } from 'react'
import {
  AlertTriangle,
  Camera,
  Check,
  Copy,
  UserCheck,
  UserX,
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
  isAlarmRecord,
  isCaptureRecord,
  isRecognitionRecord,
  type RecordItem,
  type RecordsTab,
} from '../../data/schema'
import { formatTargetType, targetTypeBadgeClass } from '../../data/target-type'
import { RecognitionComparisonPanel } from './recognition-comparison-panel'
import { buildPreviewImages, useCopyFeedback } from './record-detail-helpers'
import { RecordDetailScenePanel } from './record-detail-scene-panel'
import {
  RecordDetailCropCard,
  RecordDetailMetadataStrip,
} from './record-detail-sections'
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
  const isVehicleCapture = cap !== null && tab === 'vehicles'

  const openPreviewById = (id: string) => {
    const idx = previewImages.findIndex((item) => item.id === id)
    if (idx !== -1) setPreviewIndex(idx)
  }

  const recognitionBodyBox =
    rec && !boxesCoincide(rec.bodyBox, rec.box) ? rec.bodyBox : undefined

  const dialogTitle = rec
    ? t('detail.recognitionTitle')
    : alm
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
                  {rec && (
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
                  {alm && (
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
                  {cap && (
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
                      {formatRecordTime(record.capturedAt)}
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

                {rec && (
                  <RecognitionComparisonPanel
                    record={rec}
                    selectedCandidateIdx={selectedCandidateIdx}
                    onSelectCandidateIdx={setSelectedCandidateIdx}
                    previewImages={previewImages}
                    onOpenPreviewById={openPreviewById}
                    onOpenPreviewIndex={setPreviewIndex}
                  />
                )}

                {alm && alm.cropPath && (
                  <RecordDetailCropCard
                    path={alm.cropPath}
                    alt={alm.alarmType}
                    score={alm.score}
                    onPreview={() => openPreviewById('alarm-crop')}
                  />
                )}

                {cap && !isVehicleCapture && cap.cropPath && (
                  <RecordDetailCropCard
                    path={cap.cropPath}
                    alt={formatTargetType(t, cap.targetType)}
                    score={cap.score}
                    onPreview={() => openPreviewById('capture-crop')}
                  />
                )}

                <div className='space-y-2 rounded-lg border border-border bg-card p-4 shadow-xs'>
                  <h4 className='text-xs font-semibold tracking-wider text-muted-foreground uppercase'>
                    {alm ? t('detail.extra') : t('detail.attributes')}
                  </h4>
                  <RecordJsonViewer
                    data={
                      (alm
                        ? (alm.extra as Record<string, unknown>)
                        : rec
                          ? (rec.attributes as Record<string, unknown>)
                          : cap
                            ? (cap.attributes as Record<string, unknown>)
                            : {}) ?? {}
                    }
                  />
                </div>

                <RecordDetailMetadataStrip
                  record={record}
                  cameraName={cameraName}
                />
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
