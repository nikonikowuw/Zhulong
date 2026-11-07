import { memo } from 'react'
import { useNavigate } from '@tanstack/react-router'
import {
  ArrowRightLeft,
  CheckCircle2,
  Crown,
  Images,
  Map,
  Scan,
  User,
  UserCheck,
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
import { imageSearchApi } from '../api/image-search-api'
import { type ImageSearchMatchInfo } from '../data/schema'

interface ImageSearchCompareDialogProps {
  match: ImageSearchMatchInfo | null
  rank: number
  previewUrl: string | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

export const ImageSearchCompareDialog = memo(function ImageSearchCompareDialog({
  match,
  rank,
  previewUrl,
  open,
  onOpenChange,
}: ImageSearchCompareDialogProps) {
  const { t } = useTranslation('imageSearch')
  const navigate = useNavigate()

  if (!match) return null

  const faceUrl = imageSearchApi.getFaceImageUrl(
    match.personId,
    match.matchedFaceId
  )

  const similarityNum = match.similarity * 100
  const similarityPercent = similarityNum.toFixed(1)
  const isHighMatch = match.similarity >= 0.85
  const isMediumMatch = match.similarity >= 0.7

  const handleGoRecords = () => {
    onOpenChange(false)
    void navigate({
      to: '/records',
      search: {
        tab: 'recognitions',
        view: 'table',
        page: 1,
        pageSize: 20,
        personId: match.personId,
      },
    })
  }

  const handleGoSpatial = () => {
    onOpenChange(false)
    void navigate({
      to: '/spatial',
      search: {
        targetType: 'person',
        personId: match.personId,
      },
    })
  }

  const handleGoProfile = () => {
    onOpenChange(false)
    void navigate({
      to: '/persons',
      search: {
        personId: match.personId,
      },
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='flex max-h-[90vh] max-w-2xl flex-col overflow-hidden p-0 sm:max-h-[85vh] sm:max-w-2xl'>
        {/* 顶部头部 */}
        <DialogHeader className='shrink-0 border-b border-border/60 bg-muted/30 px-6 py-3.5'>
          <div className='flex items-center justify-between'>
            <div className='flex items-center gap-2'>
              <div className='flex h-8 w-8 items-center justify-center rounded-lg bg-primary/10 text-primary'>
                <ArrowRightLeft className='h-4 w-4' />
              </div>
              <div>
                <DialogTitle className='text-base font-bold'>
                  {t('compareDialog.title')}
                </DialogTitle>
                <DialogDescription className='text-xs text-muted-foreground'>
                  {t('compareDialog.description')}
                </DialogDescription>
              </div>
            </div>

            {/* 排名角标 */}
            <div className='flex items-center gap-1.5'>
              {rank === 1 ? (
                <Badge className='border-amber-500/40 bg-amber-500/15 text-amber-600 dark:text-amber-400'>
                  <Crown className='me-1 h-3.5 w-3.5 fill-amber-500/30' />
                  #1 {t('results.card.rankTop')}
                </Badge>
              ) : (
                <Badge variant='outline' className='font-mono text-xs'>
                  #{rank}
                </Badge>
              )}
            </div>
          </div>
        </DialogHeader>

        {/* 核心双图 1:1 比对区域 (支持视口超高时垂直滚动) */}
        <div className='flex-1 space-y-4 overflow-y-auto p-4 sm:space-y-5 sm:p-5'>
          <div className='grid grid-cols-1 items-center gap-3 sm:grid-cols-11 sm:gap-4'>
            {/* 左侧：检索原图 */}
            <div className='flex flex-col items-center gap-1.5 sm:col-span-5'>
              <div className='text-xs font-semibold text-muted-foreground'>
                {t('compareDialog.queryImage')}
              </div>
              <div className='relative flex aspect-square w-full max-w-[160px] items-center justify-center overflow-hidden rounded-2xl border-2 border-primary/30 bg-muted/30 p-1.5 shadow-xs sm:max-w-[180px]'>
                {/* HUD 边角标 */}
                <div className='pointer-events-none absolute inset-2 z-10'>
                  <div className='absolute top-0 left-0 h-3 w-3 border-t-2 border-l-2 border-primary/70' />
                  <div className='absolute top-0 right-0 h-3 w-3 border-t-2 border-r-2 border-primary/70' />
                  <div className='absolute bottom-0 left-0 h-3 w-3 border-b-2 border-l-2 border-primary/70' />
                  <div className='absolute right-0 bottom-0 h-3 w-3 border-r-2 border-b-2 border-primary/70' />
                </div>
                {previewUrl ? (
                  <img
                    src={previewUrl}
                    alt={t('compareDialog.queryImage')}
                    className='h-full w-full rounded-xl object-contain'
                  />
                ) : (
                  <Scan className='h-10 w-10 text-muted-foreground' />
                )}
              </div>
            </div>

            {/* 中间：VS 与相似度指示 */}
            <div className='flex flex-col items-center justify-center gap-2 sm:col-span-1'>
              <div className='flex h-9 w-9 items-center justify-center rounded-full border border-border/80 bg-background font-mono text-xs font-bold text-muted-foreground shadow-xs'>
                VS
              </div>
            </div>

            {/* 右侧：底库命中登记照 */}
            <div className='flex flex-col items-center gap-1.5 sm:col-span-5'>
              <div className='text-xs font-semibold text-muted-foreground'>
                {t('compareDialog.enrolledImage')}
              </div>
              <div className='relative flex aspect-square w-full max-w-[160px] items-center justify-center overflow-hidden rounded-2xl border-2 border-emerald-500/30 bg-muted/30 p-1.5 shadow-xs sm:max-w-[180px]'>
                {/* HUD 边角标 */}
                <div className='pointer-events-none absolute inset-2 z-10'>
                  <div className='absolute top-0 left-0 h-3 w-3 border-t-2 border-l-2 border-emerald-500/70' />
                  <div className='absolute top-0 right-0 h-3 w-3 border-t-2 border-r-2 border-emerald-500/70' />
                  <div className='absolute bottom-0 left-0 h-3 w-3 border-b-2 border-l-2 border-emerald-500/70' />
                  <div className='absolute right-0 bottom-0 h-3 w-3 border-r-2 border-b-2 border-emerald-500/70' />
                </div>
                <img
                  src={faceUrl}
                  alt={match.personName}
                  className='h-full w-full rounded-xl object-contain'
                  onError={(e) => {
                    // 降级占位
                    e.currentTarget.style.display = 'none'
                  }}
                />
              </div>
            </div>
          </div>

          {/* 综合比对评分仪表板 */}
          <div className='rounded-xl border border-border/60 bg-muted/20 p-4'>
            <div className='flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between'>
              <div>
                <div className='flex items-center gap-2'>
                  <h4 className='text-base font-bold text-foreground'>
                    {match.personName}
                  </h4>
                  {match.personExternalId && (
                    <Badge
                      variant='outline'
                      className='font-mono text-[11px] font-semibold'
                    >
                      {match.personExternalId}
                    </Badge>
                  )}
                </div>
                <p className='mt-0.5 text-xs text-muted-foreground'>
                  {t('compareDialog.vectorCosine')}
                </p>
              </div>

              {/* 相似度评分胶囊 */}
              <div className='flex items-center gap-3'>
                <div className='text-right'>
                  <div className='text-[10px] uppercase text-muted-foreground'>
                    {t('compareDialog.similarityScore')}
                  </div>
                  <div
                    className={`font-mono text-2xl font-black ${
                      isHighMatch
                        ? 'text-emerald-600 dark:text-emerald-400'
                        : isMediumMatch
                          ? 'text-primary'
                          : 'text-amber-600 dark:text-amber-400'
                    }`}
                  >
                    {similarityPercent}%
                  </div>
                </div>
                <div className='flex h-10 w-10 items-center justify-center rounded-xl bg-background border border-border/80 shadow-xs'>
                  {isHighMatch ? (
                    <CheckCircle2 className='h-5 w-5 text-emerald-500' />
                  ) : (
                    <User className='h-5 w-5 text-primary' />
                  )}
                </div>
              </div>
            </div>

            {/* 相似度渐变进度条 */}
            <div className='mt-3 h-2 w-full overflow-hidden rounded-full bg-muted/60'>
              <div
                className={`h-full rounded-full transition-all duration-500 ${
                  isHighMatch
                    ? 'bg-gradient-to-r from-emerald-500 to-teal-400'
                    : isMediumMatch
                      ? 'bg-gradient-to-r from-primary/80 to-primary'
                      : 'bg-gradient-to-r from-amber-500 to-amber-400'
                }`}
                style={{
                  width: `${Math.max(0, Math.min(similarityNum, 100))}%`,
                }}
              />
            </div>

            {/* 专业研判结论建议 */}
            <div
              className={`mt-3.5 flex items-start gap-2.5 rounded-lg border p-2.5 text-xs ${
                isHighMatch
                  ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-800 dark:text-emerald-300'
                  : isMediumMatch
                    ? 'border-primary/30 bg-primary/10 text-foreground'
                    : 'border-amber-500/30 bg-amber-500/10 text-amber-800 dark:text-amber-300'
              }`}
            >
              <CheckCircle2
                className={`mt-0.5 h-3.5 w-3.5 shrink-0 ${
                  isHighMatch
                    ? 'text-emerald-600 dark:text-emerald-400'
                    : isMediumMatch
                      ? 'text-primary'
                      : 'text-amber-600 dark:text-amber-400'
                }`}
              />
              <span className='leading-relaxed font-medium'>
                {isHighMatch
                  ? t('compareDialog.verdictHigh')
                  : isMediumMatch
                    ? t('compareDialog.verdictMedium')
                    : t('compareDialog.verdictLow')}
              </span>
            </div>
          </div>
        </div>

        {/* 底部业务穿透与操作 */}
        <DialogFooter className='shrink-0 border-t border-border/60 bg-muted/30 px-6 py-3.5 sm:justify-between'>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            onClick={() => onOpenChange(false)}
          >
            {t('compareDialog.close')}
          </Button>

          <div className='flex flex-wrap items-center gap-2'>
            <Button
              type='button'
              variant='outline'
              size='sm'
              className='gap-1.5'
              onClick={handleGoRecords}
            >
              <Images className='h-3.5 w-3.5' />
              <span>{t('results.card.viewRecords')}</span>
            </Button>
            <Button
              type='button'
              variant='outline'
              size='sm'
              className='gap-1.5'
              onClick={handleGoSpatial}
            >
              <Map className='h-3.5 w-3.5' />
              <span>{t('results.card.viewTrajectory')}</span>
            </Button>
            <Button
              type='button'
              size='sm'
              className='gap-1.5'
              onClick={handleGoProfile}
            >
              <UserCheck className='h-3.5 w-3.5' />
              <span>{t('results.card.viewProfile')}</span>
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
})
