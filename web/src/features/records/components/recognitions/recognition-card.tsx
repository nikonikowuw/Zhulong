import { format } from 'date-fns'
import { Eye, UserCheck, UserX } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { type Recognition } from '../../data/schema'
import { MediaImage } from '../common/media-image'

interface RecognitionCardProps {
  item: Recognition
  onSelect: (item: Recognition) => void
}

export function RecognitionCard({ item, onSelect }: RecognitionCardProps) {
  const { t } = useTranslation('records')
  const formattedTime = item.capturedAt
    ? format(new Date(item.capturedAt), 'MM-dd HH:mm:ss')
    : '-'

  const simPercent = Math.round((item.similarity || 0) * 100)

  return (
    <Card
      onClick={() => onSelect(item)}
      className='group relative cursor-pointer overflow-hidden border-border bg-card transition-all hover:border-primary/50 hover:shadow-md'
    >
      <div className='relative aspect-square w-full overflow-hidden bg-muted/40'>
        <MediaImage
          path={item.faceImagePath || item.imagePath}
          alt={item.personName || 'Face'}
          className='size-full transition-transform duration-300 group-hover:scale-105'
          fallbackText='Face Crop'
        />

        {/* 顶部匹配状态 Badge */}
        <div className='absolute start-2 top-2'>
          {item.matched ? (
            <Badge
              variant='outline'
              className='border-emerald-500/50 bg-emerald-500/10 font-medium text-emerald-600 dark:text-emerald-400'
            >
              <UserCheck className='me-1 size-3' />
              {t('card.matched')}
            </Badge>
          ) : (
            <Badge
              variant='outline'
              className='border-amber-500/50 bg-amber-500/10 font-medium text-amber-600 dark:text-amber-400'
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
            {item.personName ||
              (item.matched ? item.personId : t('card.stranger'))}
          </span>
          <span className='font-mono text-[11px] text-muted-foreground'>
            {formattedTime}
          </span>
        </div>

        <div className='flex items-center justify-between text-[11px] text-muted-foreground'>
          <span className='max-w-[130px] truncate'>{item.cameraId}</span>
          {item.trackId > 0 && (
            <span className='font-mono'>#{item.trackId}</span>
          )}
        </div>
      </CardContent>
    </Card>
  )
}
