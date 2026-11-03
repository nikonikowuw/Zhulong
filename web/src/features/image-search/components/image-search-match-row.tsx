import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { Crown, Images, Map, User, UserCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { imageSearchApi } from '../api/image-search-api'
import { type ImageSearchMatchInfo } from '../data/schema'

interface ImageSearchMatchRowProps {
  match: ImageSearchMatchInfo
  rank: number
}

export function ImageSearchMatchRow({ match, rank }: ImageSearchMatchRowProps) {
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

  // 相似度级别与主题状态
  const isTopRank = rank === 1
  const isHighMatch = match.similarity >= 0.85
  const isMediumMatch = match.similarity >= 0.7

  const getRankBadge = () => {
    if (rank === 1) {
      return (
        <span className='inline-flex items-center gap-1 rounded-md border border-amber-500/40 bg-amber-500/15 px-2 py-0.5 font-mono text-xs font-bold text-amber-600 dark:text-amber-400'>
          <Crown className='h-3.5 w-3.5 fill-amber-500/30 text-amber-500' />
          <span>#1</span>
        </span>
      )
    }
    if (rank === 2) {
      return (
        <span className='inline-flex items-center gap-1 rounded-md border border-slate-400/40 bg-slate-400/15 px-2 py-0.5 font-mono text-xs font-semibold text-slate-600 dark:text-slate-300'>
          <span>#2</span>
        </span>
      )
    }
    if (rank === 3) {
      return (
        <span className='inline-flex items-center gap-1 rounded-md border border-amber-700/30 bg-amber-700/10 px-2 py-0.5 font-mono text-xs font-semibold text-amber-800 dark:text-amber-500'>
          <span>#3</span>
        </span>
      )
    }
    return (
      <span className='shrink-0 font-mono text-xs font-medium text-muted-foreground'>
        #{rank}
      </span>
    )
  }

  return (
    <div
      className={`group relative flex flex-col gap-3 rounded-xl border p-3 shadow-2xs backdrop-blur-xs transition-all duration-200 hover:-translate-y-0.5 hover:shadow-md md:flex-row md:items-center md:justify-between md:gap-4 ${
        isTopRank
          ? 'border-amber-500/40 bg-card/85 hover:border-amber-500/70'
          : 'border-border/70 bg-card/60 hover:border-primary/50'
      }`}
    >
      {/* 顶名高亮流光顶线（仅限第 1 名） */}
      {isTopRank && (
        <div className='absolute inset-x-0 top-0 h-0.5 bg-gradient-to-r from-amber-500/30 via-amber-400 to-amber-500/30' />
      )}

      {/* 左侧：排名、底库人脸登记照与人员信息 */}
      <div className='flex min-w-0 items-center gap-3'>
        {/* 排名徽标 */}
        <div className='flex w-9 shrink-0 items-center justify-center'>
          {getRankBadge()}
        </div>

        {/* 命中底库面容头像与 HUD 角标 */}
        <div className='relative h-13 w-13 shrink-0 overflow-hidden rounded-xl border border-border/80 bg-muted/40 shadow-xs'>
          {/* HUD 四角瞄准线条 */}
          <div className='pointer-events-none absolute inset-0.5 z-10 opacity-70'>
            <div className='absolute top-0 left-0 h-1.5 w-1.5 border-t border-l border-primary/60' />
            <div className='absolute top-0 right-0 h-1.5 w-1.5 border-t border-r border-primary/60' />
            <div className='absolute bottom-0 left-0 h-1.5 w-1.5 border-b border-l border-primary/60' />
            <div className='absolute right-0 bottom-0 h-1.5 w-1.5 border-r border-b border-primary/60' />
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
              <User className='h-6 w-6 opacity-60' />
            </AvatarFallback>
          </Avatar>

          {/* 高置信度状态标记点 */}
          {isHighMatch && (
            <div
              className='absolute end-0.5 bottom-0.5 z-20 h-2 w-2 rounded-full bg-emerald-500 shadow-xs ring-2 ring-background'
              title={t('results.card.highMatch')}
            />
          )}
        </div>

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

      {/* 右侧集群：相似度比对指标与联动操作区 */}
      <div className='flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between md:justify-end md:gap-5'>
        {/* 相似度评分指标与进度条 */}
        <div className='flex min-w-[170px] flex-col gap-1.5 md:min-w-[200px]'>
          <div className='flex items-center justify-between gap-2 text-xs'>
            <div className='flex items-center gap-1.5'>
              <span className='text-[11px] font-medium whitespace-nowrap text-muted-foreground'>
                {t('results.card.similarity')}
              </span>
              {isHighMatch && (
                <span className='rounded bg-emerald-500/10 px-1.5 py-0.5 font-sans text-[10px] font-medium whitespace-nowrap text-emerald-600 dark:text-emerald-400'>
                  {t('results.card.highMatch')}
                </span>
              )}
            </div>
            <span
              className={`font-mono text-sm font-bold whitespace-nowrap tabular-nums ${
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

          {/* 相似度微型彩色进度条 */}
          <div className='h-1.5 w-full overflow-hidden rounded-full bg-muted/60'>
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
        </div>

        {/* 联动操作按钮区 */}
        <div className='flex items-center gap-1.5 border-t border-border/40 pt-2.5 sm:border-t-0 sm:pt-0'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='group/btn h-8 gap-1.5 border-border/70 bg-muted/20 px-2.5 text-xs font-medium transition-colors hover:border-primary/40 hover:bg-primary/5 hover:text-foreground'
            onClick={handleGoRecords}
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
            onClick={handleGoSpatial}
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
            onClick={handleGoProfile}
            title={t('results.card.viewProfile')}
          >
            <UserCheck className='h-3.5 w-3.5 shrink-0 text-muted-foreground group-hover/btn:text-foreground' />
            <span>{t('results.card.viewProfile')}</span>
          </Button>
        </div>
      </div>
    </div>
  )
}
