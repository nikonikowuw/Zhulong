import {
  type StatusLevel,
  statusDotClass,
  statusLevelClass,
} from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'

interface Gb28181StatusBadgeProps {
  level: StatusLevel
  label: string
  className?: string
}

/**
 * Dot-Indicator Outline 状态徽标。
 *
 * 色级一律取自 `@/lib/status-palette`，禁止就地硬编码颜色；
 * 该调色板已内置明暗双模配对。
 */
export function Gb28181StatusBadge({
  level,
  label,
  className,
}: Gb28181StatusBadgeProps) {
  return (
    <Badge
      variant='outline'
      className={cn(
        'inline-flex items-center gap-1.5 px-2 py-0.5 text-xs font-normal',
        statusLevelClass(level),
        className
      )}
    >
      <span className={cn('size-1.5 rounded-full', statusDotClass(level))} />
      {label}
    </Badge>
  )
}
