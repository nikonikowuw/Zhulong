import { useState } from 'react'
import { format } from 'date-fns'
import {
  AlertTriangle,
  Camera,
  Check,
  CheckCircle2,
  Copy,
  Cpu,
  Download,
  ExternalLink,
  Eye,
  EyeOff,
  Layers,
  Sparkles,
  UserCheck,
  UserX,
  Video,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { copyToClipboard } from '@/lib/clipboard'
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
import {
  algorithmLabel,
  type Alarm,
  type Capture,
  type Recognition,
  type RecordItem,
  type RecordsTab,
} from '../../data/schema'
import { targetTypeBadgeClass } from '../../data/target-type'
import { BoundingBoxCanvas } from '../common/bounding-box-canvas'
import { MediaImage } from '../common/media-image'
import { getGalleryImageUrl, getMediaUrl } from '../common/media-utils'
import { RecordJsonViewer } from './record-json-viewer'

export interface RecordDetailDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  record: RecordItem | null
  tab: RecordsTab
  /** `cameraId → 名称` 解析器，见 data/camera-label.ts */
  cameraName?: CameraNameLookup
}

/** 拟真车牌徽章样式（符合行业视觉习惯并兼容暗黑模式） */
function getPlateBadgeStyle(color?: string) {
  const c = color?.toLowerCase() || ''
  if (c.includes('绿') || c.includes('green')) {
    return 'border-emerald-500 bg-gradient-to-r from-emerald-600 to-green-500 text-white shadow-emerald-950/20'
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
  const [copiedPlate, setCopiedPlate] = useState(false)
  const [copiedId, setCopiedId] = useState(false)
  const [copiedExternalId, setCopiedExternalId] = useState(false)
  const [showBoxes, setShowBoxes] = useState(true)
  const [dimensions, setDimensions] = useState<{
    width: number
    height: number
  } | null>(null)

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
      setCopiedPlate(true)
      setTimeout(() => setCopiedPlate(false), 2000)
    }
  }

  const handleCopyId = async (id: string) => {
    if (!id) return
    const success = await copyToClipboard(id)
    if (success) {
      setCopiedId(true)
      setTimeout(() => setCopiedId(false), 2000)
    }
  }

  const handleCopyExternalId = async (extId: string) => {
    if (!extId) return
    const success = await copyToClipboard(extId)
    if (success) {
      setCopiedExternalId(true)
      setTimeout(() => setCopiedExternalId(false), 2000)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='flex max-h-[92vh] w-[96vw] flex-col overflow-hidden p-0 sm:max-h-[88vh] sm:max-w-4xl lg:max-w-5xl xl:max-w-6xl'>
        {/* 顶部标题与快速概览 */}
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
                    {cap.targetType}
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

        {/* 核心工作区：左右两栏专业研判台 */}
        <div className='flex flex-1 flex-col overflow-hidden lg:flex-row'>
          {/* 左栏：现场全景视口（暗色沉浸式监视屏 + 操作工具栏） */}
          <div className='flex flex-1 flex-col border-b border-border bg-neutral-950 p-4 lg:w-7/12 lg:border-r lg:border-b-0'>
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

              {/* 原图快捷操作组 */}
              <div className='flex items-center gap-1'>
                <Button
                  type='button'
                  variant='ghost'
                  size='sm'
                  className='h-7 px-2 text-[11px] text-neutral-400 hover:bg-neutral-800 hover:text-neutral-200'
                  onClick={() => setShowBoxes(!showBoxes)}
                  title={
                    showBoxes ? t('detail.hideBoxes') : t('detail.showBoxes')
                  }
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
                        download={`capture-${record.id}.jpg`}
                        title={t('detail.downloadImage')}
                      >
                        <Download className='size-3.5' />
                      </a>
                    </Button>
                  </>
                )}
              </div>
            </div>

            <div className='flex flex-1 items-center justify-center overflow-hidden rounded-lg border border-neutral-800 bg-black/90 p-2'>
              <BoundingBoxCanvas
                imagePath={record.imagePath}
                alt={t('detail.fullImage')}
                showBoxes={showBoxes}
                onDimensionsLoaded={setDimensions}
                primaryBox={record.box}
                primaryLabel={
                  isRecognition && rec
                    ? rec.personName ||
                      (rec.matched ? t('card.matched') : t('card.stranger'))
                    : isAlarm && alm
                      ? alm.alarmType
                      : isCapture && cap
                        ? cap.targetType
                        : undefined
                }
                secondaryBox={isRecognition && rec ? rec.bodyBox : undefined}
                secondaryLabel={
                  isRecognition && rec?.bodyBox
                    ? t('detail.bodyBoxLabel')
                    : undefined
                }
                className='max-h-[560px]'
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

          {/* 右栏：智能研判详情面板（独立平滑滚动） */}
          <ScrollArea className='flex-1 lg:w-5/12'>
            <div className='space-y-4 p-5'>
              {/* 1. 车辆结构化证据与切片 */}
              {isVehicleCapture && cap && (
                <div className='space-y-3 rounded-lg border border-border bg-card p-4 shadow-xs'>
                  <div className='flex items-center justify-between'>
                    <h4 className='text-xs font-semibold tracking-wider text-muted-foreground uppercase'>
                      {t('detail.vehicleCrop')}
                    </h4>
                    {cap.score > 0 && (
                      <Badge
                        variant='outline'
                        className='font-mono text-[11px]'
                      >
                        {t('detail.confidence')} {Math.round(cap.score * 100)}%
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
                            className='text-xs font-medium capitalize'
                          >
                            {plateColor}
                          </Badge>
                        )}
                        {vehicleType && (
                          <Badge
                            variant='outline'
                            className='text-xs text-muted-foreground capitalize'
                          >
                            {vehicleType}
                          </Badge>
                        )}
                      </div>
                      <Button
                        type='button'
                        variant='ghost'
                        size='sm'
                        className='h-8 px-2 text-xs text-muted-foreground hover:text-foreground'
                        onClick={() => void handleCopyPlate(plateNo)}
                      >
                        {copiedPlate ? (
                          <Check className='me-1 size-3.5 text-emerald-500' />
                        ) : (
                          <Copy className='me-1 size-3.5' />
                        )}
                        {copiedPlate
                          ? t('detail.copied')
                          : t('detail.copyPlate')}
                      </Button>
                    </div>
                  )}

                  {/* 车辆切片高清展示 */}
                  {cap.cropPath && (
                    <div className='group relative flex h-36 w-full items-center justify-center overflow-hidden rounded-md border border-border bg-muted/30 p-2'>
                      <MediaImage
                        path={cap.cropPath}
                        alt={t('vehicle.cropAlt')}
                        className='size-full'
                        imageClassName='object-contain'
                      />
                      <div className='absolute end-2 top-2 opacity-0 transition-opacity group-hover:opacity-100'>
                        <Button
                          type='button'
                          variant='secondary'
                          size='sm'
                          className='size-7 p-0 shadow-sm'
                          asChild
                        >
                          <a
                            href={getMediaUrl(cap.cropPath)}
                            target='_blank'
                            rel='noreferrer'
                            title={t('detail.openInNewTab')}
                          >
                            <ExternalLink className='size-3.5' />
                          </a>
                        </Button>
                      </div>
                    </div>
                  )}
                </div>
              )}

              {/* 2. 人脸识别专属比对区：现场切片 vs 底库照片 */}
              {isRecognition && rec && (
                <div className='space-y-4 rounded-lg border border-border bg-card p-4 shadow-xs'>
                  <div className='flex items-center justify-between'>
                    <h4 className='flex items-center gap-1.5 text-xs font-semibold tracking-wider text-muted-foreground uppercase'>
                      <Sparkles className='size-3.5 text-primary' />
                      {t('detail.comparison')}
                    </h4>
                    {rec.matched && rec.similarity > 0 && (
                      <Badge
                        variant='outline'
                        className={cn(
                          'font-mono text-xs',
                          statusLevelClass('active')
                        )}
                      >
                        <span
                          className={cn(
                            'me-1 size-1.5 rounded-full',
                            statusDotClass('active')
                          )}
                        />
                        {t('card.similarity')}:{' '}
                        {Math.round(rec.similarity * 100)}%
                      </Badge>
                    )}
                  </div>

                  {/* 比对工作台 */}
                  <div className='relative grid grid-cols-2 gap-4 rounded-lg border border-border/60 bg-muted/20 p-3'>
                    {/* 现场切片 */}
                    <div className='flex flex-col items-center space-y-2'>
                      <span className='text-[11px] font-medium text-muted-foreground'>
                        {t('detail.snapshot')}
                      </span>
                      <div className='size-24 overflow-hidden rounded-md border border-border bg-muted/40 shadow-xs'>
                        <MediaImage
                          path={rec.faceImagePath}
                          alt={t('detail.snapshot')}
                          className='size-full'
                          imageClassName='object-contain'
                          fallbackText='Face Crop'
                        />
                      </div>
                    </div>

                    {/* 底库对标照片 */}
                    <div className='flex flex-col items-center space-y-2'>
                      <span className='text-[11px] font-medium text-muted-foreground'>
                        {t('detail.gallery')}
                      </span>
                      <div className='size-24 overflow-hidden rounded-md border border-border bg-muted/40 shadow-xs'>
                        <MediaImage
                          src={getGalleryImageUrl(
                            rec.personId,
                            rec.matchedFaceId
                          )}
                          alt={t('detail.gallery')}
                          className='size-full'
                          imageClassName='object-contain'
                          fallbackText={t('detail.noGallery')}
                        />
                      </div>
                    </div>
                  </div>

                  {/* 身份核验结果 */}
                  {rec.matched ? (
                    <div className='rounded-md border border-emerald-500/30 bg-emerald-500/5 p-3 text-xs dark:border-emerald-500/40 dark:bg-emerald-500/10'>
                      <div className='flex items-center justify-between'>
                        <div className='flex items-center gap-1.5 font-semibold text-foreground'>
                          <CheckCircle2 className='size-4 text-emerald-500' />
                          <span>{rec.personName || t('card.matched')}</span>
                        </div>
                        {rec.personExternalId && (
                          <Button
                            type='button'
                            variant='ghost'
                            size='sm'
                            className='h-6 px-1.5 text-[11px] text-muted-foreground hover:text-foreground'
                            onClick={() =>
                              void handleCopyExternalId(rec.personExternalId)
                            }
                          >
                            {copiedExternalId ? (
                              <Check className='me-1 size-3 text-emerald-500' />
                            ) : (
                              <Copy className='me-1 size-3' />
                            )}
                            {rec.personExternalId}
                          </Button>
                        )}
                      </div>
                      {rec.personId && (
                        <div className='mt-1 font-mono text-[11px] text-muted-foreground'>
                          {t('detail.personId')}: {rec.personId}
                        </div>
                      )}
                    </div>
                  ) : (
                    <div className='rounded-md border border-border bg-muted/30 p-2.5 text-center text-xs text-muted-foreground'>
                      {t('detail.verificationUnmatched')}
                    </div>
                  )}

                  {/* Top-N 候选人列表 */}
                  {rec.candidates && rec.candidates.length > 0 && (
                    <div className='space-y-2 pt-1'>
                      <div className='flex items-center justify-between text-xs font-medium text-muted-foreground'>
                        <span>{t('detail.candidates')}</span>
                        <span className='font-mono text-[11px]'>
                          ({rec.candidates.length})
                        </span>
                      </div>
                      <div className='grid grid-cols-1 gap-2'>
                        {rec.candidates.map((cand, idx) => {
                          const candSim = Math.round(cand.similarity * 100)
                          return (
                            <div
                              key={`cand-${cand.faceId || cand.personId || idx}`}
                              className='relative overflow-hidden rounded-md border border-border/80 bg-muted/30 p-2.5 text-xs transition-colors hover:bg-muted/50'
                            >
                              <div className='flex items-center justify-between gap-2'>
                                <div className='min-w-0 flex-1 space-y-0.5'>
                                  <div className='truncate font-semibold text-foreground'>
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
                                  {candSim}%
                                </Badge>
                              </div>
                              {/* 相似度比例进度条 */}
                              <div className='mt-1.5 h-1 w-full overflow-hidden rounded-full bg-muted'>
                                <div
                                  className='h-full rounded-full bg-primary/70 transition-all'
                                  style={{ width: `${candSim}%` }}
                                />
                              </div>
                            </div>
                          )
                        })}
                      </div>
                    </div>
                  )}
                </div>
              )}

              {/* 3. 告警切片小图 */}
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
                        {t('detail.confidence')} {Math.round(alm.score * 100)}%
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
                        {t('detail.confidence')} {Math.round(cap.score * 100)}%
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

              {/* 5. 结构化属性 / 额外参数 (双模式查看器) */}
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

              {/* 6. 溯源元数据信息 */}
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

        {/* 底部操作区 */}
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
              onClick={() => void handleCopyId(record.id)}
            >
              {record.id}
              {copiedId ? (
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
  )
}
