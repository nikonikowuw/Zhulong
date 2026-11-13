import { Crown } from 'lucide-react'
import { STATUS_LEVEL_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'

interface ImageSearchRankBadgeProps {
  rank: number
  variant?: 'card' | 'row'
}

export function ImageSearchRankBadge({
  rank,
  variant = 'card',
}: ImageSearchRankBadgeProps) {
  const sizeClass =
    variant === 'row' ? 'px-2 py-0.5 text-xs' : 'px-1.5 py-0.5 text-[11px]'

  if (rank === 1) {
    return (
      <span
        className={cn(
          'inline-flex items-center gap-1 rounded-md border font-mono font-bold shadow-xs backdrop-blur-xs',
          sizeClass,
          STATUS_LEVEL_CLASS.warning
        )}
      >
        <Crown className='h-3 w-3 fill-amber-500/30 text-amber-500' />
        <span>#1</span>
      </span>
    )
  }

  if (rank === 2) {
    return (
      <span
        className={cn(
          'inline-flex items-center gap-1 rounded-md border font-mono font-semibold shadow-xs backdrop-blur-xs',
          sizeClass,
          STATUS_LEVEL_CLASS.inactive
        )}
      >
        <span>#2</span>
      </span>
    )
  }

  if (rank === 3) {
    return (
      <span
        className={cn(
          'inline-flex items-center gap-1 rounded-md border border-primary/30 bg-primary/10 font-mono font-semibold text-primary shadow-xs backdrop-blur-xs dark:border-primary/40 dark:bg-primary/20',
          sizeClass
        )}
      >
        <span>#3</span>
      </span>
    )
  }

  if (variant === 'row') {
    return (
      <span className='shrink-0 font-mono text-xs font-medium text-muted-foreground'>
        #{rank}
      </span>
    )
  }

  return (
    <span className='rounded-md border border-border/70 bg-background/80 px-1.5 py-0.5 font-mono text-[10px] font-medium text-muted-foreground backdrop-blur-xs'>
      #{rank}
    </span>
  )
}
