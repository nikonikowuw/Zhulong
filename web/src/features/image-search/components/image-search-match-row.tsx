import { memo } from 'react'
import { ArrowRightLeft, Images, Map, User, UserCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { STATUS_DOT_CLASS, STATUS_LEVEL_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { imageSearchApi } from '../api/image-search-api'
import { getSimilarityPresentation } from '../data/match-utils'
import { type ImageSearchMatchInfo } from '../data/schema'
import { usePersonDeepLinks } from '../hooks/use-person-deep-links'
import { ImageSearchRankBadge } from './image-search-rank-badge'

interface ImageSearchMatchRowProps {
  match: ImageSearchMatchInfo
  rank: number
  onInspect?: (personId: string) => void
}

export const ImageSearchMatchRow = memo(function ImageSearchMatchRow({
  match,
  rank,
  onInspect,
}: ImageSearchMatchRowProps) {
  const { t } = useTranslation('imageSearch')
  const { goRecords, goSpatial, goProfile } = usePersonDeepLinks(match.personId)

  const faceUrl = imageSearchApi.getFaceImageUrl(
    match.personId,
    match.matchedFaceId
  )

  const { percentWidth, similarityPercent, isHighMatch, textClass, barClass } =
    getSimilarityPresentation(match.similarity)
  const isTopRank = rank === 1

  return (
    <div
      data-testid='image-search-match-row'
      className={cn(
        'group relative flex flex-col gap-3 rounded-xl border p-3 shadow-2xs backdrop-blur-xs transition-all duration-200 hover:-translate-y-0.5 hover:shadow-md md:flex-row md:items-center md:justify-between md:gap-4',
        isTopRank
          ? 'border-amber-500/40 bg-card/85 hover:border-amber-500/70'
          : 'border-border/70 bg-card/60 hover:border-primary/50'
      )}
    >
      {/* 顶名高亮流光顶线（仅限第 1 名） */}
      {isTopRank && (
        <div className='absolute inset-x-0 top-0 h-0.5 bg-gradient-to-r from-amber-500/30 via-amber-400 to-amber-500/30' />
      )}

      {/* 起始侧：排名、底库人脸登记照与人员信息 */}
      <div className='flex min-w-0 items-center gap-3'>
        <div className='flex w-9 shrink-0 items-center justify-center'>
          <ImageSearchRankBadge rank={rank} variant='row' />
        </div>

        {/* 命中底库面容头像按钮（支持键盘聚焦触发 1:1 比对） */}
        <button
          type='button'
          disabled={!onInspect}
          onClick={() => onInspect?.(match.personId)}
          aria-label={`${t('results.compare')}: ${match.personName}`}
          title={t('results.compare')}
          className='relative h-13 w-13 shrink-0 cursor-pointer overflow-hidden rounded-xl border border-border/80 bg-muted/40 shadow-xs transition-transform group-hover:scale-105 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none disabled:cursor-default'
        >
          <div className='pointer-events-none absolute inset-0.5 z-10 opacity-70'>
            <div className='absolute start-0 top-0 h-1.5 w-1.5 border-s border-t border-primary/60' />
            <div className='absolute end-0 top-0 h-1.5 w-1.5 border-e border-t border-primary/60' />
            <div className='absolute start-0 bottom-0 h-1.5 w-1.5 border-s border-b border-primary/60' />
            <div className='absolute end-0 bottom-0 h-1.5 w-1.5 border-e border-b border-primary/60' />
          </div>

          <Avatar className='h-full w-full rounded-xl'>
            <AvatarImage
              src={faceUrl}
              alt={t('results.card.matchedFaceAlt')}
              className='h-full w-full object-cover transition-transform duration-300'
            />
            <AvatarFallback className='rounded-xl bg-muted/60 text-muted-foreground'>
              <User className='h-6 w-6 opacity-60' />
            </AvatarFallback>
          </Avatar>

          {isHighMatch && (
            <div
              className={cn(
                'absolute end-0.5 bottom-0.5 z-20 h-2 w-2 rounded-full shadow-xs ring-2 ring-background',
                STATUS_DOT_CLASS.active
              )}
              title={t('results.card.highMatch')}
            />
          )}
        </button>

        {/* 核心身份信息：姓名与外部工号 */}
        <div className='flex min-w-0 flex-col justify-center gap-1'>
          <h3
            className='truncate text-sm font-bold text-foreground transition-colors group-hover:text-primary'
            title={match.personName}
          >
            {match.personName}
          </h3>

          <div className='flex items-center gap-1.5 text-xs text-muted-foreground'>
            {match.personExternalId ? (
              <Badge
                variant='outline'
                className='border-border/70 bg-muted/30 px-1.5 py-0 font-mono text-[10px] text-foreground'
              >
                {match.personExternalId}
              </Badge>
            ) : (
              <span className='text-[11px] text-muted-foreground/70'>
                {t('results.card.unboundId')}
              </span>
            )}
          </div>
        </div>
      </div>

      {/* 结束侧：相似度比对指标与联动操作区 */}
      <div className='flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between md:justify-end md:gap-5'>
        <div className='flex min-w-[170px] flex-col gap-1.5 md:min-w-[200px]'>
          <div className='flex items-center justify-between gap-2 text-xs'>
            <div className='flex items-center gap-1.5'>
              <span className='text-[11px] font-medium whitespace-nowrap text-muted-foreground'>
                {t('results.card.similarity')}
              </span>
              {isHighMatch && (
                <Badge
                  variant='outline'
                  className={cn(
                    'gap-1 px-1.5 py-0 font-sans text-[10px] font-normal whitespace-nowrap',
                    STATUS_LEVEL_CLASS.active
                  )}
                >
                  <span
                    className={cn(
                      'inline-block h-1.5 w-1.5 rounded-full',
                      STATUS_DOT_CLASS.active
                    )}
                  />
                  {t('results.card.highMatch')}
                </Badge>
              )}
            </div>
            <span
              className={cn(
                'font-mono text-sm font-bold whitespace-nowrap tabular-nums',
                textClass
              )}
            >
              {similarityPercent}%
            </span>
          </div>

          <div className='h-1.5 w-full overflow-hidden rounded-full bg-muted/60'>
            <div
              className={cn(
                'h-full rounded-full transition-all duration-500',
                barClass
              )}
              style={{ width: `${percentWidth}%` }}
            />
          </div>
        </div>

        {/* 联动操作按钮区 */}
        <div className='flex items-center gap-1.5 border-t border-border/40 pt-2.5 sm:border-t-0 sm:pt-0'>
          {onInspect && (
            <Button
              type='button'
              variant='outline'
              size='sm'
              className='group/btn h-8 gap-1.5 border-border/70 bg-muted/20 px-2.5 text-xs font-medium transition-colors hover:border-primary/40 hover:bg-primary/5 hover:text-foreground'
              onClick={() => onInspect(match.personId)}
              title={t('results.compare')}
            >
              <ArrowRightLeft className='h-3.5 w-3.5 shrink-0 text-muted-foreground group-hover/btn:text-primary' />
              <span>{t('results.compare')}</span>
            </Button>
          )}
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='group/btn h-8 gap-1.5 border-border/70 bg-muted/20 px-2.5 text-xs font-medium transition-colors hover:border-primary/40 hover:bg-primary/5 hover:text-foreground'
            onClick={goRecords}
            title={t('results.card.viewRecords')}
          >
            <Images className='h-3.5 w-3.5 shrink-0 text-muted-foreground group-hover/btn:text-foreground' />
            <span>{t('results.card.viewRecords')}</span>
          </Button>
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='group/btn h-8 gap-1.5 border-border/70 bg-muted/20 px-2.5 text-xs font-medium transition-colors hover:border-primary/40 hover:bg-primary/5 hover:text-foreground'
            onClick={goSpatial}
            title={t('results.card.viewTrajectory')}
          >
            <Map className='h-3.5 w-3.5 shrink-0 text-muted-foreground group-hover/btn:text-foreground' />
            <span>{t('results.card.viewTrajectory')}</span>
          </Button>
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='group/btn h-8 gap-1.5 border-border/70 bg-muted/20 px-2.5 text-xs font-medium transition-colors hover:border-primary/40 hover:bg-primary/5 hover:text-foreground'
            onClick={goProfile}
            title={t('results.card.viewProfile')}
          >
            <UserCheck className='h-3.5 w-3.5 shrink-0 text-muted-foreground group-hover/btn:text-foreground' />
            <span>{t('results.card.viewProfile')}</span>
          </Button>
        </div>
      </div>
    </div>
  )
})
