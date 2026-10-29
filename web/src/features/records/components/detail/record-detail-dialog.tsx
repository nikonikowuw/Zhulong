import { useState } from 'react'
import { format } from 'date-fns'
import {
  AlertTriangle,
  Camera,
  Check,
  Copy,
  Layers,
  Sparkles,
  UserCheck,
  UserX,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { copyToClipboard } from '@/lib/clipboard'
import { statusLevelClass } from '@/lib/status-palette'
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
import {
  algorithmLabel,
  type Alarm,
  type Capture,
  type Recognition,
  type RecordItem,
  type RecordsTab,
} from '../../data/schema'
import { BoundingBoxCanvas } from '../common/bounding-box-canvas'
import { MediaImage } from '../common/media-image'
import { getGalleryImageUrl } from '../common/media-utils'
import { RecordJsonViewer } from './record-json-viewer'

export interface RecordDetailDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  record: RecordItem | null
  tab: RecordsTab
  /** `cameraId → 名称` 解析器，见 data/camera-label.ts */
  cameraName?: CameraNameLookup
}

function getPlateBadgeStyle(color?: string) {
  const c = color?.toLowerCase() || ''
  if (c.includes('绿') || c.includes('green')) {
    return 'border-emerald-400 bg-gradient-to-r from-emerald-600 to-green-500 text-white shadow-emerald-950/20'
  }
  if (c.includes('黄') || c.includes('yellow')) {
    return 'border-amber-500 bg-amber-400 font-black text-neutral-950 shadow-amber-950/20'
  }
  if (c.includes('白') || c.includes('white')) {
    return 'border-neutral-300 bg-neutral-100 font-bold text-neutral-950 shadow-neutral-950/20'
  }
  if (c.includes('黑') || c.includes('black')) {
    return 'border-neutral-700 bg-neutral-900 font-bold text-white shadow-black/40'
  }
  return 'border-blue-500 bg-blue-600 text-white shadow-blue-950/20'
}

export function RecordDetailDialog({
  open,
  onOpenChange,
  record,
  tab,
  cameraName,
}: RecordDetailDialogProps) {
  const { t } = useTranslation('records')
  const [copied, setCopied] = useState(false)

  if (!record) return null

  const isRecognition = tab === 'recognitions'
  const isAlarm = tab === 'alarms'
  const isVehicleCapture = tab === 'vehicles'
  const isCapture = tab === 'captures' || isVehicleCapture

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
      : isVehicleCapture
        ? t('detail.vehicleTitle')
        : t('detail.captureTitle')

  // 车辆特有感知属性解析
  const vehicleAttrs =
    isVehicleCapture && cap?.attributes
      ? (cap.attributes as Record<string, unknown>)
      : null
  const plateNo =
    typeof vehicleAttrs?.plate_number === 'string'
      ? vehicleAttrs.plate_number
      : ''
  const plateColor =
    typeof vehicleAttrs?.plate_color === 'string'
      ? vehicleAttrs.plate_color
      : ''
  const vehicleType =
    typeof vehicleAttrs?.vehicle_type === 'string'
      ? vehicleAttrs.vehicle_type
      : ''

  const handleCopyPlate = async (text: string) => {
    if (!text) return
    const success = await copyToClipboard(text)
    if (success) {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='flex max-h-[92vh] w-[96vw] flex-col overflow-hidden p-0 sm:max-h-[88vh] sm:max-w-4xl lg:max-w-5xl xl:max-w-6xl'>
        {/* 顶部标题与快速概览 */}
        <DialogHeader className='border-b border-border px-6 py-4'>
          <div className='flex items-center justify-between gap-4'>
            <div className='space-y-1.5'>
              <DialogTitle className='flex items-center gap-2.5 text-lg font-bold'>
                <span>{dialogTitle}</span>
                {isRecognition && rec && (
                  <Badge
                    variant='outline'
                    className={cn(
                      'font-medium',
                      statusLevelClass(rec.matched ? 'active' : 'warning')
                    )}
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
                  {t('detail.cameraId')}:{' '}
                  <strong>{cameraLabel(record.cameraId, cameraName)}</strong>
                </span>
                <span>•</span>
                <span>
                  {t('detail.capturedAt')}: <strong>{capturedTime}</strong>
                </span>
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>

        {/* 核心工作区：左右两栏专业研判台 */}
        <div className='flex flex-1 flex-col overflow-hidden lg:flex-row'>
          {/* 左栏：现场全景视口（暗色沉浸式监视屏） */}
          <div className='flex flex-1 flex-col border-b border-border bg-neutral-950 p-4 lg:w-7/12 lg:border-r lg:border-b-0'>
            <div className='mb-2 flex items-center justify-between text-xs text-muted-foreground'>
              <h4 className='flex items-center gap-1.5 font-semibold tracking-wider text-neutral-300 uppercase'>
                <Layers className='size-3.5 text-primary' />
                {t('detail.fullImage')}
              </h4>
              <span className='font-mono text-[11px] text-neutral-400'>
                {t('detail.highlightNotice')}
              </span>
            </div>

            <div className='flex flex-1 items-center justify-center overflow-hidden rounded-lg border border-neutral-800 bg-black/90 p-2'>
              <BoundingBoxCanvas
                imagePath={record.imagePath}
                alt={t('detail.fullImage')}
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
                className='max-h-[560px]'
              />
            </div>
          </div>

          {/* 右栏：智能研判详情面板（独立平滑滚动） */}
          <ScrollArea className='flex-1 lg:w-5/12'>
            <div className='space-y-4 p-5'>
              {/* 1. 车辆结构化证据与切片 */}
              {isVehicleCapture && cap && (
                <div className='space-y-3 rounded-lg border border-border bg-card p-4'>
                  <div className='flex items-center justify-between'>
                    <h4 className='text-xs font-semibold tracking-wider text-muted-foreground uppercase'>
                      {t('detail.vehicleCrop')}
                    </h4>
                    {cap.score > 0 && (
                      <Badge
                        variant='outline'
                        className='font-mono text-[11px]'
                      >
                        置信度 {Math.round(cap.score * 100)}%
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
                            className='text-xs font-medium'
                          >
                            {plateColor}
                          </Badge>
                        )}
                        {vehicleType && (
                          <Badge
                            variant='outline'
                            className='text-xs text-muted-foreground'
                          >
                            {vehicleType}
                          </Badge>
                        )}
                      </div>
                      <Button
                        variant='ghost'
                        size='sm'
                        className='h-8 px-2 text-xs text-muted-foreground hover:text-foreground'
                        onClick={() => void handleCopyPlate(plateNo)}
                      >
                        {copied ? (
                          <Check className='me-1 size-3.5 text-emerald-500' />
                        ) : (
                          <Copy className='me-1 size-3.5' />
                        )}
                        {copied ? t('detail.copied') : t('detail.copyPlate')}
                      </Button>
                    </div>
                  )}

                  {/* 车辆切片高清展示 */}
                  {cap.cropPath && (
                    <div className='flex h-36 w-full items-center justify-center overflow-hidden rounded-md border border-border bg-muted/30 p-2'>
                      <MediaImage
                        path={cap.cropPath}
                        alt={t('vehicle.cropAlt')}
                        className='size-full'
                        imageClassName='object-contain'
                      />
                    </div>
                  )}
                </div>
              )}

              {/* 2. 人脸识别专属比对区：现场切片 vs 底库照片 */}
              {isRecognition && rec && (
                <div className='space-y-3 rounded-lg border border-border bg-card p-4'>
                  <div className='flex items-center justify-between'>
                    <h4 className='flex items-center gap-1.5 text-xs font-semibold tracking-wider text-muted-foreground uppercase'>
                      <Sparkles className='size-3.5 text-primary' />
                      {t('detail.comparison')}
                    </h4>
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

                  <div className='grid grid-cols-2 gap-3'>
                    {/* 现场切片 */}
                    <div className='flex flex-col items-center space-y-1.5 rounded-md border border-border bg-muted/20 p-2.5'>
                      <span className='text-[11px] font-medium text-muted-foreground'>
                        {t('detail.snapshot')}
                      </span>
                      <div className='size-24 overflow-hidden rounded-md border border-border bg-muted/40'>
                        <MediaImage
                          path={rec.faceImagePath}
                          alt='Live Face Crop'
                          className='size-full'
                          imageClassName='object-contain'
                          fallbackText='Face Crop'
                        />
                      </div>
                    </div>

                    {/* 底库对标照片 */}
                    <div className='flex flex-col items-center space-y-1.5 rounded-md border border-border bg-muted/20 p-2.5'>
                      <span className='text-[11px] font-medium text-muted-foreground'>
                        {t('detail.gallery')}
                      </span>
                      <div className='size-24 overflow-hidden rounded-md border border-border bg-muted/40'>
                        <MediaImage
                          src={getGalleryImageUrl(
                            rec.personId,
                            rec.matchedFaceId
                          )}
                          alt='Gallery Target'
                          className='size-full'
                          imageClassName='object-contain'
                          fallbackText={t('detail.noGallery')}
                        />
                      </div>
                    </div>
                  </div>

                  {/* Top-N 候选人列表 */}
                  {rec.candidates && rec.candidates.length > 0 && (
                    <div className='mt-2 space-y-2'>
                      <div className='text-xs font-medium text-muted-foreground'>
                        {t('detail.candidates')}
                      </div>
                      <div className='grid grid-cols-1 gap-1.5 sm:grid-cols-2'>
                        {rec.candidates.map((cand, idx) => (
                          <div
                            key={`cand-${cand.faceId || idx}`}
                            className='flex items-center justify-between rounded-md border border-border/80 bg-muted/30 p-2 text-xs'
                          >
                            <div className='max-w-[130px] space-y-0.5 truncate'>
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
                <div className='space-y-2 rounded-lg border border-border bg-card p-4'>
                  <div className='flex items-center justify-between'>
                    <h4 className='text-xs font-semibold tracking-wider text-muted-foreground uppercase'>
                      {t('detail.snapshot')}
                    </h4>
                    {alm.score > 0 && (
                      <Badge
                        variant='outline'
                        className='font-mono text-[11px]'
                      >
                        {Math.round(alm.score * 100)}%
                      </Badge>
                    )}
                  </div>
                  <div className='flex h-36 w-full items-center justify-center overflow-hidden rounded-md border border-border bg-muted/40 p-2'>
                    <MediaImage
                      path={alm.cropPath}
                      alt={alm.alarmType}
                      className='size-full'
                      imageClassName='object-contain'
                    />
                  </div>
                </div>
              )}

              {/* 4. 普通目标抓拍切片小图 */}
              {isCapture && !isVehicleCapture && cap && cap.cropPath && (
                <div className='space-y-2 rounded-lg border border-border bg-card p-4'>
                  <div className='flex items-center justify-between'>
                    <h4 className='text-xs font-semibold tracking-wider text-muted-foreground uppercase'>
                      {t('detail.snapshot')}
                    </h4>
                    {cap.score > 0 && (
                      <Badge
                        variant='outline'
                        className='font-mono text-[11px]'
                      >
                        {Math.round(cap.score * 100)}%
                      </Badge>
                    )}
                  </div>
                  <div className='flex h-36 w-full items-center justify-center overflow-hidden rounded-md border border-border bg-muted/40 p-2'>
                    <MediaImage
                      path={cap.cropPath}
                      alt={cap.targetType}
                      className='size-full'
                      imageClassName='object-contain'
                    />
                  </div>
                </div>
              )}

              {/* 5. 结构化属性 / 额外参数 */}
              <div className='space-y-2 rounded-lg border border-border bg-card p-4'>
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

              {/* 6. 溯源元数据信息 */}
              <div className='grid grid-cols-2 gap-2.5 rounded-lg border border-border bg-muted/30 p-3 text-xs text-muted-foreground sm:grid-cols-2'>
                <div>
                  <span className='block text-[10px] font-medium uppercase'>
                    {t('detail.cameraId')}
                  </span>
                  <span className='truncate font-medium text-foreground'>
                    {cameraLabel(record.cameraId, cameraName)}
                  </span>
                </div>
                {'algorithmId' in record && (
                  <div>
                    <span className='block text-[10px] font-medium uppercase'>
                      {t('detail.algorithmId')}
                    </span>
                    <span className='truncate font-mono text-foreground'>
                      {algorithmLabel(record) || '-'}
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
        </div>

        {/* 底部操作区 */}
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
