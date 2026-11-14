import { memo, type KeyboardEvent } from 'react'
import { Eye, UserCheck, UserX } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { statusLevelClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { cameraLabel, type CameraNameLookup } from '../../data/camera-label'
import { formatRecordTime } from '../../data/format-time'
import { type Recognition } from '../../data/schema'
import { MediaImage } from '../common/media-image'

interface RecognitionCardProps {
  item: Recognition
  cameraName?: CameraNameLookup
  onSelect: (item: Recognition) => void
}

export const RecognitionCard = memo(function RecognitionCard({
  item,
  cameraName,
  onSelect,
}: RecognitionCardProps) {
  const { t } = useTranslation('records')
  const formattedTime = formatRecordTime(item.capturedAt, 'MM-dd HH:mm:ss')
  const simPercent = Math.round((item.similarity || 0) * 100)
  const displayName =
    item.personName || (item.matched ? item.personId : t('card.stranger'))

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      onSelect(item)
    }
  }

  return (
    <Card
      role='button'
      tabIndex={0}
      aria-label={`${displayName || t('card.stranger')} - ${t('card.viewDetail')}`}
      onClick={() => onSelect(item)}
      onKeyDown={handleKeyDown}
      className='group relative cursor-pointer overflow-hidden border-border bg-card transition-all hover:border-primary/50 hover:shadow-md focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-hidden'
    >
      <div className='relative aspect-square w-full overflow-hidden bg-muted/40'>
        <MediaImage
          path={item.faceImagePath || item.imagePath}
          alt={item.personName || t('card.fallbackFaceShort')}
          className='size-full transition-transform duration-300 group-hover:scale-105'
          fallbackText={t('card.fallbackFace')}
        />

        {/* 顶部匹配状态 Badge */}
        <div className='absolute start-2 top-2'>
          {item.matched ? (
            <Badge
              variant='outline'
              className={cn('font-medium', statusLevelClass('active'))}
            >
              <UserCheck className='me-1 size-3' />
              {t('card.matched')}
            </Badge>
          ) : (
            <Badge
              variant='outline'
              className={cn('font-medium', statusLevelClass('warning'))}
            >
              <UserX className='me-1 size-3' />
              {t('card.stranger')}
            </Badge>
          )}
        </div>

        {/* 相似度标签 */}
        {item.matched && item.similarity > 0 && (
          <div className='absolute end-2 top-2 rounded bg-black/70 px-1.5 py-0.5 font-mono text-[11px] font-semibold text-white'>
            {simPercent}%
          </div>
        )}

        {/* 悬停快捷指示 */}
        <div className='absolute inset-0 flex items-center justify-center bg-black/30 opacity-0 backdrop-blur-[1px] transition-opacity group-hover:opacity-100'>
          <span className='flex items-center gap-1 rounded-full bg-background/90 px-3 py-1 text-xs font-medium text-foreground shadow-sm'>
            <Eye className='size-3.5' />
            {t('card.viewDetail')}
          </span>
        </div>
      </div>

      <CardContent className='space-y-1.5 p-3'>
        <div className='flex items-center justify-between text-xs'>
          <span className='max-w-[120px] truncate font-semibold text-foreground'>
            {displayName}
          </span>
          <span className='font-mono text-[11px] text-muted-foreground'>
            {formattedTime}
          </span>
        </div>

        <div className='flex items-center justify-between text-[11px] text-muted-foreground'>
          <span className='max-w-[130px] truncate'>
            {cameraLabel(item.cameraId, cameraName)}
          </span>
          {item.trackId > 0 && (
            <span className='font-mono'>#{item.trackId}</span>
          )}
        </div>
      </CardContent>
    </Card>
  )
})
