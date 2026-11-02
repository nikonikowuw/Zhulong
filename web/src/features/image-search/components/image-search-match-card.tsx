import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { Crown, Images, Map, User, UserCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { imageSearchApi } from '../api/image-search-api'
import { type ImageSearchMatchInfo } from '../data/schema'

interface ImageSearchMatchCardProps {
  match: ImageSearchMatchInfo
  rank: number
}

export function ImageSearchMatchCard({
  match,
  rank,
}: ImageSearchMatchCardProps) {
  const { t } = useTranslation('imageSearch')
  const navigate = useNavigate()
  const [imgError, setImgError] = useState(false)

  const faceUrl = imageSearchApi.getFaceImageUrl(
    match.personId,
    match.matchedFaceId
  )

  const similarityNum = match.similarity * 100
  const similarityPercent = similarityNum.toFixed(1)

  const handleGoRecords = () => {
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
    void navigate({
      to: '/spatial',
      search: {
        targetType: 'person',
        personId: match.personId,
      },
    })
  }

  const handleGoProfile = () => {
    void navigate({
      to: '/persons',
      search: {
        personId: match.personId,
      },
    })
  }

  // 相似度级别与主题色
  const isTopRank = rank === 1
  const isHighMatch = match.similarity >= 0.85
  const isMediumMatch = match.similarity >= 0.7

  const getRankBadge = () => {
    if (rank === 1) {
      return (
        <span className='inline-flex items-center gap-1 rounded-md border border-amber-500/40 bg-amber-500/15 px-1.5 py-0.5 font-mono text-[11px] font-bold text-amber-600 dark:text-amber-400'>
          <Crown className='h-3 w-3 fill-amber-500/30 text-amber-500' />
          <span>#1</span>
        </span>
      )
    }
    if (rank === 2) {
      return (
        <span className='inline-flex items-center gap-1 rounded-md border border-slate-400/40 bg-slate-400/15 px-1.5 py-0.5 font-mono text-[11px] font-semibold text-slate-600 dark:text-slate-300'>
          <span>#2</span>
        </span>
      )
    }
    if (rank === 3) {
      return (
        <span className='inline-flex items-center gap-1 rounded-md border border-amber-700/30 bg-amber-700/10 px-1.5 py-0.5 font-mono text-[11px] font-semibold text-amber-800 dark:text-amber-500'>
          <span>#3</span>
        </span>
      )
    }
    return (
      <span className='shrink-0 font-mono text-[11px] text-muted-foreground'>
        #{rank}
      </span>
    )
  }

  return (
    <Card
      className={`group relative flex flex-col justify-between overflow-hidden rounded-xl border bg-card/75 p-4 shadow-xs backdrop-blur-xs transition-all duration-200 hover:-translate-y-1 hover:shadow-lg ${
        isTopRank
          ? 'border-amber-500/40 hover:border-amber-500/70'
          : 'border-border/70 hover:border-primary/50'
      }`}
    >
      {/* 顶名高亮流光顶线（仅限第 1 名） */}
      {isTopRank && (
        <div className='absolute inset-x-0 top-0 h-0.5 bg-gradient-to-r from-amber-500/30 via-amber-400 to-amber-500/30' />
      )}

      <div>
        <div className='flex gap-3.5'>
          {/* 命中底库面容头像与 HUD 角标 */}
          <div className='relative h-20 w-20 shrink-0 overflow-hidden rounded-xl border border-border/80 bg-muted/40 shadow-xs'>
            {/* HUD 四角瞄准线条 */}
            <div className='pointer-events-none absolute inset-1 z-10 opacity-70'>
              <div className='absolute top-0 left-0 h-2 w-2 border-t-2 border-l-2 border-primary/60' />
              <div className='absolute top-0 right-0 h-2 w-2 border-t-2 border-r-2 border-primary/60' />
              <div className='absolute bottom-0 left-0 h-2 w-2 border-b-2 border-l-2 border-primary/60' />
              <div className='absolute right-0 bottom-0 h-2 w-2 border-r-2 border-b-2 border-primary/60' />
            </div>

            <Avatar className='h-full w-full rounded-xl'>
              {!imgError ? (
                <AvatarImage
                  src={faceUrl}
                  alt={t('results.card.matchedFaceAlt')}
                  className='h-full w-full object-cover transition-transform duration-300 group-hover:scale-105'
                  onError={() => setImgError(true)}
                />
              ) : null}
              <AvatarFallback className='rounded-xl bg-muted/60 text-muted-foreground'>
                <User className='h-8 w-8 opacity-60' />
              </AvatarFallback>
            </Avatar>

            {/* 高置信度状态标记 */}
            {isHighMatch && (
              <div
                className='absolute end-1 bottom-1 z-20 h-2 w-2 rounded-full bg-emerald-500 shadow-xs ring-2 ring-background'
                title={t('results.card.highMatch')}
              />
            )}
          </div>

          {/* 核心信息区 */}
          <div className='flex min-w-0 flex-1 flex-col justify-between'>
            <div>
              <div className='flex items-center justify-between gap-1'>
                <h3
                  className='truncate text-sm font-bold text-foreground transition-colors group-hover:text-primary'
                  title={match.personName}
                >
                  {match.personName}
                </h3>
                {getRankBadge()}
              </div>

              <div className='mt-1 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground'>
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

            {/* 相似度评分指标与置信胶囊 */}
            <div className='mt-2 border-t border-border/40 pt-1.5'>
              <div className='flex items-center justify-between text-xs'>
                <span className='text-[11px] font-medium text-muted-foreground'>
                  {t('results.card.similarity')}
                </span>
                <div className='flex items-center gap-1.5'>
                  {isHighMatch && (
                    <span className='hidden rounded bg-emerald-500/10 px-1 py-0.5 font-sans text-[10px] font-medium text-emerald-600 sm:inline-block dark:text-emerald-400'>
                      {t('results.card.highMatch')}
                    </span>
                  )}
                  <span
                    className={`font-mono text-sm font-bold ${
                      isHighMatch
                        ? 'text-emerald-600 dark:text-emerald-400'
                        : isMediumMatch
                          ? 'text-primary'
                          : 'text-amber-600 dark:text-amber-400'
                    }`}
                  >
                    {similarityPercent}%
                  </span>
                </div>
              </div>

              {/* 相似度微型彩色进度条 */}
              <div className='mt-1.5 h-1.5 w-full overflow-hidden rounded-full bg-muted/60'>
                <div
                  className={`h-full rounded-full transition-all duration-500 ${
                    isHighMatch
                      ? 'bg-gradient-to-r from-emerald-500 to-teal-400'
                      : isMediumMatch
                        ? 'bg-gradient-to-r from-primary/80 to-primary'
                        : 'bg-gradient-to-r from-amber-500 to-amber-400'
                  }`}
                  style={{ width: `${Math.max(0, Math.min(similarityNum, 100))}%` }}
                />
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* 联动操作按钮区 */}
      <div className='mt-3.5 grid grid-cols-3 gap-1.5 border-t border-border/60 pt-2.5'>
        <Button
          type='button'
          variant='outline'
          size='sm'
          className='group/btn h-7.5 gap-1 border-border/60 bg-muted/20 px-1.5 text-[11px] font-medium transition-colors hover:border-primary/40 hover:bg-primary/5 hover:text-foreground'
          onClick={handleGoRecords}
          title={t('results.card.viewRecords')}
        >
          <Images className='h-3.5 w-3.5 shrink-0 text-muted-foreground group-hover/btn:text-foreground' />
          <span className='truncate'>{t('results.card.viewRecords')}</span>
        </Button>
        <Button
          type='button'
          variant='outline'
          size='sm'
          className='group/btn h-7.5 gap-1 border-border/60 bg-muted/20 px-1.5 text-[11px] font-medium transition-colors hover:border-primary/40 hover:bg-primary/5 hover:text-foreground'
          onClick={handleGoSpatial}
          title={t('results.card.viewTrajectory')}
        >
          <Map className='h-3.5 w-3.5 shrink-0 text-muted-foreground group-hover/btn:text-foreground' />
          <span className='truncate'>{t('results.card.viewTrajectory')}</span>
        </Button>
        <Button
          type='button'
          variant='outline'
          size='sm'
          className='group/btn h-7.5 gap-1 border-border/60 bg-muted/20 px-1.5 text-[11px] font-medium transition-colors hover:border-primary/40 hover:bg-primary/5 hover:text-foreground'
          onClick={handleGoProfile}
          title={t('results.card.viewProfile')}
        >
          <UserCheck className='h-3.5 w-3.5 shrink-0 text-muted-foreground group-hover/btn:text-foreground' />
          <span className='truncate'>{t('results.card.viewProfile')}</span>
        </Button>
      </div>
    </Card>
  )
}
