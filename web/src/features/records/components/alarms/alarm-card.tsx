import { format } from 'date-fns'
import { AlertTriangle, Eye } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { type Alarm } from '../../data/schema'
import { MediaImage } from '../common/media-image'

interface AlarmCardProps {
  item: Alarm
  onSelect: (item: Alarm) => void
}

export function AlarmCard({ item, onSelect }: AlarmCardProps) {
  const { t } = useTranslation('records')
  const formattedTime = item.capturedAt
    ? format(new Date(item.capturedAt), 'MM-dd HH:mm:ss')
    : '-'

  const scorePercent = Math.round((item.score || 0) * 100)

  return (
    <Card
      onClick={() => onSelect(item)}
      className='group relative cursor-pointer overflow-hidden border-border bg-card transition-all hover:border-destructive/50 hover:shadow-md'
    >
      <div className='relative aspect-square w-full overflow-hidden bg-muted/40'>
        <MediaImage
          path={item.cropPath || item.imagePath}
          alt={item.alarmType}
          className='size-full transition-transform duration-300 group-hover:scale-105'
          fallbackText='Alarm Snapshot'
        />

        {/* 顶部告警类型 Badge */}
        <div className='absolute start-2 top-2'>
          <Badge
            variant='destructive'
            className='text-destructive-foreground flex items-center gap-1 bg-destructive/90 font-medium shadow-sm'
          >
            <AlertTriangle className='size-3' />
            {item.alarmType}
          </Badge>
        </div>

        {/* 得分 */}
        {item.score > 0 && (
          <div className='absolute end-2 top-2 rounded bg-black/70 px-1.5 py-0.5 font-mono text-[11px] font-semibold text-white'>
            {scorePercent}%
          </div>
        )}

        <div className='absolute inset-0 flex items-center justify-center bg-black/30 opacity-0 backdrop-blur-[1px] transition-opacity group-hover:opacity-100'>
          <span className='flex items-center gap-1 rounded-full bg-background/90 px-3 py-1 text-xs font-medium text-foreground shadow-sm'>
            <Eye className='size-3.5' />
            {t('card.viewDetail')}
          </span>
        </div>
      </div>

      <CardContent className='space-y-1.5 p-3'>
        <div className='flex items-center justify-between text-xs'>
          <span className='max-w-[120px] truncate font-semibold text-destructive'>
            {item.alarmType}
          </span>
          <span className='font-mono text-[11px] text-muted-foreground'>
            {formattedTime}
          </span>
        </div>

        <div className='flex items-center justify-between text-[11px] text-muted-foreground'>
          <span className='max-w-[130px] truncate'>{item.cameraId}</span>
          {item.pts > 0 && (
            <span className='font-mono text-[10px] opacity-75'>
              pts:{item.pts}
            </span>
          )}
        </div>
      </CardContent>
    </Card>
  )
}
