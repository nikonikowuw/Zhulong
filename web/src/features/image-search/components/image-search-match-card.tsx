import { memo } from 'react'
import { useNavigate } from '@tanstack/react-router'
import {
  ArrowRightLeft,
  Crown,
  Images,
  Map,
  User,
  UserCheck,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { imageSearchApi } from '../api/image-search-api'
import { type ImageSearchMatchInfo } from '../data/schema'

interface ImageSearchMatchCardProps {
  match: ImageSearchMatchInfo
  rank: number
  onInspect: (match: ImageSearchMatchInfo, rank: number) => void
}

export const ImageSearchMatchCard = memo(function ImageSearchMatchCard({
  match,
  rank,
  onInspect,
}: ImageSearchMatchCardProps) {
  const { t } = useTranslation('imageSearch')
  const navigate = useNavigate()

  const faceUrl = imageSearchApi.getFaceImageUrl(
    match.personId,
    match.matchedFaceId
  )

  const similarityNum = match.similarity * 100
  const similarityPercent = similarityNum.toFixed(1)

  const isTopRank = rank === 1
  const isHighMatch = match.similarity >= 0.85
  const isMediumMatch = match.similarity >= 0.7

  const handleGoRecords = (e: React.MouseEvent) => {
    e.stopPropagation()
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

  const handleGoSpatial = (e: React.MouseEvent) => {
    e.stopPropagation()
    void navigate({
      to: '/spatial',
      search: {
        targetType: 'person',
        personId: match.personId,
      },
    })
  }

  const handleGoProfile = (e: React.MouseEvent) => {
    e.stopPropagation()
    void navigate({
      to: '/persons',
      search: {
        personId: match.personId,
      },
    })
  }

  const getRankBadge = () => {
    if (rank === 1) {
      return (
        <span className='inline-flex items-center gap-1 rounded-md border border-amber-500/40 bg-amber-500/20 px-1.5 py-0.5 font-mono text-[11px] font-bold text-amber-600 shadow-xs backdrop-blur-xs dark:text-amber-400'>
          <Crown className='h-3 w-3 fill-amber-500/30 text-amber-500' />
          <span>#1</span>
        </span>
      )
    }
    if (rank === 2) {
      return (
        <span className='inline-flex items-center gap-1 rounded-md border border-slate-400/40 bg-slate-400/20 px-1.5 py-0.5 font-mono text-[11px] font-semibold text-slate-700 shadow-xs backdrop-blur-xs dark:text-slate-200'>
          <span>#2</span>
        </span>
      )
    }
    if (rank === 3) {
      return (
        <span className='inline-flex items-center gap-1 rounded-md border border-amber-700/30 bg-amber-700/20 px-1.5 py-0.5 font-mono text-[11px] font-semibold text-amber-800 shadow-xs backdrop-blur-xs dark:text-amber-300'>
          <span>#3</span>
        </span>
      )
    }
    return (
      <span className='rounded-md border border-border/70 bg-background/80 px-1.5 py-0.5 font-mono text-[10px] font-medium text-muted-foreground backdrop-blur-xs'>
        #{rank}
      </span>
    )
  }

  return (
    <div
      onClick={() => onInspect(match, rank)}
      className={`group relative flex cursor-pointer flex-col overflow-hidden rounded-2xl border transition-all duration-200 hover:-translate-y-1 hover:shadow-lg ${
        isTopRank
          ? 'border-amber-500/50 bg-card shadow-xs ring-1 ring-amber-500/20 hover:border-amber-500'
          : 'border-border/70 bg-card hover:border-primary/50'
      }`}
    >
      {/* 顶名流光顶线 */}
      {isTopRank && (
        <div className='absolute inset-x-0 top-0 z-20 h-1 bg-gradient-to-r from-amber-500/30 via-amber-400 to-amber-500/30' />
      )}

      {/* 图片展示区 (大头像，视觉排查核心) */}
      <div className='relative aspect-square w-full overflow-hidden bg-muted/40'>
        {/* HUD 四角瞄准线条 */}
        <div className='pointer-events-none absolute inset-2 z-10 opacity-60 transition-opacity group-hover:opacity-100'>
          <div className='absolute top-0 left-0 h-2 w-2 border-t-2 border-l-2 border-primary/70' />
          <div className='absolute top-0 right-0 h-2 w-2 border-t-2 border-r-2 border-primary/70' />
          <div className='absolute bottom-0 left-0 h-2 w-2 border-b-2 border-l-2 border-primary/70' />
          <div className='absolute right-0 bottom-0 h-2 w-2 border-r-2 border-b-2 border-primary/70' />
        </div>

        {/* 左上角排名 */}
        <div className='absolute start-2.5 top-2.5 z-20'>{getRankBadge()}</div>

        {/* 右上角相似度胶囊 */}
        <div
          title={`${t('results.card.similarity')}: ${similarityPercent}%`}
          className='absolute end-2.5 top-2.5 z-20 flex items-center gap-1 rounded-full border border-background/40 bg-background/90 px-2 py-0.5 font-mono text-xs font-bold shadow-xs backdrop-blur-xs'
        >
          <span
            className={
              isHighMatch
                ? 'text-emerald-600 dark:text-emerald-400'
                : isMediumMatch
                  ? 'text-primary'
                  : 'text-amber-600 dark:text-amber-400'
            }
          >
            {similarityPercent}%
          </span>
        </div>

        {/* 头像 */}
        <Avatar className='h-full w-full rounded-none'>
          <AvatarImage
            src={faceUrl}
            alt={match.personName}
            className='h-full w-full object-cover transition-transform duration-300 group-hover:scale-105'
          />
          <AvatarFallback className='rounded-none bg-muted/50 text-muted-foreground'>
            <User className='h-12 w-12 opacity-40' />
          </AvatarFallback>
        </Avatar>

        {/* 鼠标悬停时的“1:1 比对”快速入口遮罩 */}
        <div className='absolute inset-0 z-10 flex items-center justify-center bg-black/40 opacity-0 backdrop-blur-[1px] transition-opacity duration-200 group-hover:opacity-100'>
          <span className='inline-flex items-center gap-1.5 rounded-full border border-white/30 bg-background/90 px-3 py-1 text-xs font-semibold text-foreground shadow-md backdrop-blur-xs'>
            <ArrowRightLeft className='h-3.5 w-3.5 text-primary' />
            <span>{t('results.compare')}</span>
          </span>
        </div>
      </div>

      {/* 底部信息区 */}
      <div className='flex flex-1 flex-col justify-between p-3'>
        <div className='min-w-0 space-y-1.5'>
          <div className='flex items-center justify-between gap-1'>
            <h4
              className='truncate text-sm font-bold text-foreground transition-colors group-hover:text-primary'
              title={match.personName}
            >
              {match.personName}
            </h4>
            <span className='text-[10px] font-medium text-muted-foreground'>
              {t('results.card.similarity')}
            </span>
          </div>

          <div className='flex items-center gap-1.5'>
            {match.personExternalId ? (
              <Badge
                variant='outline'
                className='border-border/70 bg-muted/30 px-1.5 py-0 font-mono text-[10px] text-foreground'
              >
                {match.personExternalId}
              </Badge>
            ) : (
              <span className='text-[10px] text-muted-foreground/70'>
                {t('results.card.unboundId')}
              </span>
            )}
          </div>
        </div>

        {/* 相似度进度条 */}
        <div className='mt-2.5 h-1.5 w-full overflow-hidden rounded-full bg-muted/60'>
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

        {/* 底部业务穿透快捷按钮群（采用均分网格，图标+文字完整显示，绝无遮挡） */}
        <div className='mt-3 grid grid-cols-3 gap-1.5 border-t border-border/40 pt-2.5'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='h-7.5 w-full gap-1 px-1 text-[11px] font-medium text-muted-foreground hover:border-primary/40 hover:bg-primary/5 hover:text-foreground'
            onClick={handleGoRecords}
            title={t('results.card.viewRecords')}
            aria-label={t('results.card.viewRecords')}
          >
            <Images className='h-3 w-3 shrink-0 text-primary' />
            <span className='truncate'>{t('results.card.viewRecords')}</span>
          </Button>
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='h-7.5 w-full gap-1 px-1 text-[11px] font-medium text-muted-foreground hover:border-primary/40 hover:bg-primary/5 hover:text-foreground'
            onClick={handleGoSpatial}
            title={t('results.card.viewTrajectory')}
            aria-label={t('results.card.viewTrajectory')}
          >
            <Map className='h-3 w-3 shrink-0 text-primary' />
            <span className='truncate'>{t('results.card.viewTrajectory')}</span>
          </Button>
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='h-7.5 w-full gap-1 px-1 text-[11px] font-medium text-muted-foreground hover:border-primary/40 hover:bg-primary/5 hover:text-foreground'
            onClick={handleGoProfile}
            title={t('results.card.viewProfile')}
            aria-label={t('results.card.viewProfile')}
          >
            <UserCheck className='h-3 w-3 shrink-0 text-primary' />
            <span className='truncate'>{t('results.card.viewProfile')}</span>
          </Button>
        </div>
      </div>
    </div>
  )
})
