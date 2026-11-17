import { Film } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { STATUS_TEXT_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { type ClipInfo } from '../../data/schema'

interface ClipIndicatorProps {
  clip: ClipInfo | null | undefined
  className?: string
}

/**
 * 列表行 / 卡片上的「有录像」标记。
 *
 * 只要事件引用了片段就显示（含 `recording`），失败的片段不算「有录像」。
 */
export function ClipIndicator({ clip, className }: ClipIndicatorProps) {
  const { t } = useTranslation('records')
  if (!clip || clip.status === 'failed') return null

  const label =
    clip.status === 'recording' ? t('clip.recordingBadge') : t('clip.hasClip')

  return (
    <span
      role='img'
      aria-label={label}
      title={label}
      className={cn(
        'inline-flex shrink-0 items-center',
        clip.status === 'recording'
          ? cn(STATUS_TEXT_CLASS.warning, 'animate-pulse')
          : STATUS_TEXT_CLASS.active,
        className
      )}
    >
      <Film className='size-3.5' />
    </span>
  )
}
