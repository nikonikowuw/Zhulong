import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { Images, Map, User, UserCheck } from 'lucide-react'
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

  const similarityPercent = (match.similarity * 100).toFixed(1)

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

  return (
    <Card className='flex flex-col justify-between overflow-hidden border-border/80 bg-card p-4 shadow-xs transition-shadow hover:shadow-md'>
      <div className='flex gap-3.5'>
        {/* 命中底库面容头像 */}
        <Avatar className='h-20 w-20 shrink-0 rounded-lg border border-border bg-muted/30'>
          {!imgError ? (
            <AvatarImage
              src={faceUrl}
              alt={t('results.card.matchedFaceAlt')}
              className='object-cover'
              onError={() => setImgError(true)}
            />
          ) : null}
          <AvatarFallback className='rounded-lg text-muted-foreground'>
            <User className='h-8 w-8' />
          </AvatarFallback>
        </Avatar>

        {/* 信息区 */}
        <div className='flex min-w-0 flex-1 flex-col justify-between'>
          <div>
            <div className='flex items-center justify-between gap-1'>
              <h3 className='truncate text-sm font-semibold text-foreground'>
                {match.personName}
              </h3>
              <span className='shrink-0 font-mono text-[11px] text-muted-foreground'>
                #{rank}
              </span>
            </div>

            <div className='mt-1 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground'>
              {match.personExternalId ? (
                <Badge
                  variant='outline'
                  className='px-1.5 py-0 font-mono text-[11px]'
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

          {/* 相似度评分指标 */}
          <div className='mt-2 flex items-center justify-between border-t border-border/50 pt-1.5'>
            <span className='text-xs text-muted-foreground'>
              {t('results.card.similarity')}
            </span>
            <span className='font-mono text-sm font-bold text-primary'>
              {similarityPercent}%
            </span>
          </div>
        </div>
      </div>

      {/* 联动操作按钮区 */}
      <div className='mt-3.5 grid grid-cols-3 gap-1.5 border-t border-border/60 pt-2.5'>
        <Button
          type='button'
          variant='outline'
          size='sm'
          className='h-7 gap-1 px-1.5 text-[11px]'
          onClick={handleGoRecords}
          title={t('results.card.viewRecords')}
        >
          <Images className='h-3 w-3 shrink-0' />
          <span className='truncate'>{t('results.card.viewRecords')}</span>
        </Button>
        <Button
          type='button'
          variant='outline'
          size='sm'
          className='h-7 gap-1 px-1.5 text-[11px]'
          onClick={handleGoSpatial}
          title={t('results.card.viewTrajectory')}
        >
          <Map className='h-3 w-3 shrink-0' />
          <span className='truncate'>{t('results.card.viewTrajectory')}</span>
        </Button>
        <Button
          type='button'
          variant='outline'
          size='sm'
          className='h-7 gap-1 px-1.5 text-[11px]'
          onClick={handleGoProfile}
          title={t('results.card.viewProfile')}
        >
          <UserCheck className='h-3 w-3 shrink-0' />
          <span className='truncate'>{t('results.card.viewProfile')}</span>
        </Button>
      </div>
    </Card>
  )
}
