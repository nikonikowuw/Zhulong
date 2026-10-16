import { format } from 'date-fns'
import {
  AlertTriangle,
  Camera,
  Layers,
  Sparkles,
  UserCheck,
  UserX,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
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
import { Separator } from '@/components/ui/separator'
import {
  type Alarm,
  type Capture,
  type Recognition,
  type RecordItem,
} from '../../data/schema'
import { BoundingBoxCanvas } from '../common/bounding-box-canvas'
import { MediaImage } from '../common/media-image'
import { RecordJsonViewer } from './record-json-viewer'

export interface RecordDetailDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  record: RecordItem | null
  tab: 'recognitions' | 'alarms' | 'captures'
}

export function RecordDetailDialog({
  open,
  onOpenChange,
  record,
  tab,
}: RecordDetailDialogProps) {
  const { t } = useTranslation('records')

  if (!record) return null

  const isRecognition = tab === 'recognitions'
  const isAlarm = tab === 'alarms'
  const isCapture = tab === 'captures'

  const rec = isRecognition ? (record as Recognition) : null
  const alm = isAlarm ? (record as Alarm) : null
  const cap = isCapture ? (record as Capture) : null

  const capturedTime = record.capturedAt
    ? format(new Date(record.capturedAt), 'yyyy-MM-dd HH:mm:ss')
    : '-'

  const dialogTitle = isRecognition
    ? t('detail.recognitionTitle')
    : isAlarm
      ? t('detail.alarmTitle')
      : t('detail.captureTitle')

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='flex max-h-[92vh] max-w-4xl flex-col overflow-hidden p-0'>
        <DialogHeader className='border-b border-border px-6 pt-5 pb-3'>
          <div className='flex items-center justify-between'>
            <div className='space-y-1'>
              <DialogTitle className='flex items-center gap-2 text-lg font-bold'>
                {dialogTitle}
                {isRecognition && rec && (
                  <Badge
                    variant='outline'
                    className={
                      rec.matched
                        ? 'border-emerald-500/50 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
                        : 'border-amber-500/50 bg-amber-500/10 text-amber-600 dark:text-amber-400'
                    }
                  >
                    {rec.matched ? (
                      <UserCheck className='me-1 size-3' />
                    ) : (
                      <UserX className='me-1 size-3' />
                    )}
                    {rec.matched
                      ? rec.personName || t('card.matched')
                      : t('card.stranger')}
                  </Badge>
                )}
                {isAlarm && alm && (
                  <Badge
                    variant='destructive'
                    className='flex items-center gap-1'
                  >
                    <AlertTriangle className='size-3' />
                    {alm.alarmType}
                  </Badge>
                )}
                {isCapture && cap && (
                  <Badge
                    variant='secondary'
                    className='flex items-center gap-1 capitalize'
                  >
                    <Camera className='size-3' />
                    {cap.targetType}
                  </Badge>
                )}
              </DialogTitle>
              <DialogDescription className='flex items-center gap-3 text-xs text-muted-foreground'>
                <span>
                  {t('detail.cameraId')}: <strong>{record.cameraId}</strong>
                </span>
                <span>•</span>
                <span>
                  {t('detail.capturedAt')}: <strong>{capturedTime}</strong>
                </span>
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>

        <ScrollArea className='flex-1 p-6'>
          <div className='space-y-6'>
            {/* 1. 原图与目标标注框渲染 */}
            <div className='space-y-2'>
              <div className='flex items-center justify-between'>
                <h4 className='flex items-center gap-1.5 text-xs font-semibold tracking-wider text-muted-foreground uppercase'>
                  <Layers className='size-3.5' />
                  {t('detail.fullImage')}
                </h4>
              </div>

              <div className='flex justify-center rounded-lg border border-border bg-black/95 p-2'>
                <BoundingBoxCanvas
                  imagePath={record.imagePath}
                  alt='Full Scene'
                  primaryBox={record.box}
                  primaryLabel={
                    isRecognition && rec
                      ? rec.personName || (rec.matched ? 'Matched' : 'Stranger')
                      : isAlarm && alm
                        ? alm.alarmType
                        : isCapture && cap
                          ? cap.targetType
                          : undefined
                  }
                  secondaryBox={isRecognition && rec ? rec.bodyBox : undefined}
                  secondaryLabel={
                    isRecognition && rec?.bodyBox ? 'Body' : undefined
                  }
                  className='max-h-[460px]'
                />
              </div>
            </div>

            <Separator />

            {/* 2. 识别专属比对区：现场切片 vs 底库照片 */}
            {isRecognition && rec && (
              <div className='space-y-3'>
                <h4 className='flex items-center gap-1.5 text-xs font-semibold tracking-wider text-muted-foreground uppercase'>
                  <Sparkles className='size-3.5' />
                  {t('detail.comparison')}
                </h4>

                <div className='grid grid-cols-1 gap-4 sm:grid-cols-2'>
                  {/* 现场切片 */}
                  <div className='flex flex-col items-center space-y-2 rounded-lg border border-border bg-card p-3'>
                    <span className='text-xs font-medium text-muted-foreground'>
                      {t('detail.snapshot')}
                    </span>
                    <div className='size-28 overflow-hidden rounded-md border border-border bg-muted/40'>
                      <MediaImage
                        path={rec.faceImagePath}
                        alt='Live Face Crop'
                        className='size-full'
                        fallbackText='Face Crop'
                      />
                    </div>
                  </div>

                  {/* 底库对标照片 */}
                  <div className='flex flex-col items-center space-y-2 rounded-lg border border-border bg-card p-3'>
                    <span className='text-xs font-medium text-muted-foreground'>
                      {t('detail.gallery')}
                    </span>
                    <div className='size-28 overflow-hidden rounded-md border border-border bg-muted/40'>
                      <MediaImage
                        path={rec.galleryImagePath}
                        alt='Gallery Target'
                        className='size-full'
                        fallbackText={t('detail.noGallery')}
                      />
                    </div>
                    {rec.matched && rec.similarity > 0 && (
                      <Badge
                        variant='outline'
                        className='border-emerald-500/40 font-mono text-xs text-emerald-600'
                      >
                        {t('card.similarity')}:{' '}
                        {Math.round(rec.similarity * 100)}%
                      </Badge>
                    )}
                  </div>
                </div>

                {/* Top-N 候选人列表 */}
                {rec.candidates && rec.candidates.length > 0 && (
                  <div className='mt-3 space-y-2'>
                    <div className='text-xs font-medium text-muted-foreground'>
                      {t('detail.candidates')}
                    </div>
                    <div className='grid grid-cols-1 gap-2 sm:grid-cols-2 md:grid-cols-3'>
                      {rec.candidates.map((cand, idx) => (
                        <div
                          key={`cand-${cand.faceId || idx}`}
                          className='flex items-center justify-between rounded-md border border-border/80 bg-muted/30 p-2 text-xs'
                        >
                          <div className='max-w-[140px] space-y-0.5 truncate'>
                            <div className='truncate font-semibold'>
                              {cand.personName ||
                                cand.personId ||
                                `Candidate #${idx + 1}`}
                            </div>
                            <div className='truncate font-mono text-[10px] text-muted-foreground'>
                              {cand.faceId || cand.personId}
                            </div>
                          </div>
                          <Badge
                            variant='secondary'
                            className='font-mono text-[11px] font-semibold'
                          >
                            {Math.round(cand.similarity * 100)}%
                          </Badge>
                        </div>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            )}

            {/* 3. 告警切片小图 */}
            {isAlarm && alm && alm.cropPath && (
              <div className='space-y-2'>
                <h4 className='text-xs font-semibold tracking-wider text-muted-foreground uppercase'>
                  {t('detail.snapshot')}
                </h4>
                <div className='size-32 overflow-hidden rounded-md border border-border bg-muted/40'>
                  <MediaImage
                    path={alm.cropPath}
                    alt={alm.alarmType}
                    className='size-full'
                  />
                </div>
              </div>
            )}

            {/* 4. 抓拍切片小图 */}
            {isCapture && cap && cap.cropPath && (
              <div className='space-y-2'>
                <h4 className='text-xs font-semibold tracking-wider text-muted-foreground uppercase'>
                  {t('detail.snapshot')}
                </h4>
                <div className='size-32 overflow-hidden rounded-md border border-border bg-muted/40'>
                  <MediaImage
                    path={cap.cropPath}
                    alt={cap.targetType}
                    className='size-full'
                  />
                </div>
              </div>
            )}

            <Separator />

            {/* 5. 结构化属性 / 额外参数 */}
            <div className='space-y-2'>
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

            {/* 6. 元数据底部状态条 */}
            <div className='grid grid-cols-2 gap-2 rounded-lg bg-muted/30 p-3 text-xs text-muted-foreground sm:grid-cols-4'>
              <div>
                <span className='block text-[10px] font-medium uppercase'>
                  {t('detail.cameraId')}
                </span>
                <span className='font-mono text-foreground'>
                  {record.cameraId}
                </span>
              </div>
              {'algorithmId' in record && (
                <div>
                  <span className='block text-[10px] font-medium uppercase'>
                    {t('detail.algorithmId')}
                  </span>
                  <span className='font-mono text-foreground'>
                    {record.algorithmId || '-'}
                  </span>
                </div>
              )}
              {'pts' in record && (
                <div>
                  <span className='block text-[10px] font-medium uppercase'>
                    {t('detail.pts')}
                  </span>
                  <span className='font-mono text-foreground'>
                    {record.pts > 0 ? record.pts : '-'}
                  </span>
                </div>
              )}
              {'trackId' in record && (
                <div>
                  <span className='block text-[10px] font-medium uppercase'>
                    {t('card.trackId')}
                  </span>
                  <span className='font-mono text-foreground'>
                    {record.trackId > 0 ? `#${record.trackId}` : '-'}
                  </span>
                </div>
              )}
            </div>
          </div>
        </ScrollArea>

        <DialogFooter className='border-t border-border bg-muted/10 px-6 py-3'>
          <Button
            variant='outline'
            size='sm'
            onClick={() => onOpenChange(false)}
          >
            {t('detail.close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
