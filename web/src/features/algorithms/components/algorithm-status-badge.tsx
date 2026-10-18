import { useTranslation } from 'react-i18next'
import { STATUS_DOT_CLASS, STATUS_LEVEL_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { type AlgorithmStatus } from '../data/schema'

/**
 * §1.3.3 经典四级状态语义调色板（必须成对具备双模支持）。
 * 类名统一取自 `@/lib/status-palette`，禁止就地改色。
 */
const LEVEL_ACTIVE = STATUS_LEVEL_CLASS.active
const LEVEL_WARNING = STATUS_LEVEL_CLASS.warning
const LEVEL_DESTRUCTIVE = STATUS_LEVEL_CLASS.destructive
const LEVEL_INACTIVE = STATUS_LEVEL_CLASS.inactive

const DOT_ACTIVE = STATUS_DOT_CLASS.active
/** 规范允许「进行中 / 同步中」档的圆点叠加 animate-pulse。 */
const DOT_WARNING = cn(STATUS_DOT_CLASS.warning, 'animate-pulse')
const DOT_DESTRUCTIVE = STATUS_DOT_CLASS.destructive
const DOT_INACTIVE = STATUS_DOT_CLASS.inactive

interface StatusLevel {
  dotClass: string
  badgeClass: string
  labelKey: string
}

/**
 * 7 个算法状态收敛到规范四档：
 * ACTIVE/RUNNING → 活跃；STARTING/ROLLING_BACK → 进行中；CRASHED/CRASH_LOOP → 破坏性；DISABLED → 未激活。
 */
const STATUS_LEVEL: Record<AlgorithmStatus, StatusLevel> = {
  ACTIVE: {
    dotClass: DOT_ACTIVE,
    badgeClass: LEVEL_ACTIVE,
    labelKey: 'statusActive',
  },
  RUNNING: {
    dotClass: DOT_ACTIVE,
    badgeClass: LEVEL_ACTIVE,
    labelKey: 'statusRunning',
  },
  STARTING: {
    dotClass: DOT_WARNING,
    badgeClass: LEVEL_WARNING,
    labelKey: 'statusStarting',
  },
  DISABLED: {
    dotClass: DOT_INACTIVE,
    badgeClass: LEVEL_INACTIVE,
    labelKey: 'statusDisabled',
  },
  CRASHED: {
    dotClass: DOT_DESTRUCTIVE,
    badgeClass: LEVEL_DESTRUCTIVE,
    labelKey: 'statusCrashed',
  },
  CRASH_LOOP: {
    dotClass: DOT_DESTRUCTIVE,
    badgeClass: LEVEL_DESTRUCTIVE,
    labelKey: 'statusCrashLoop',
  },
  ROLLING_BACK: {
    dotClass: DOT_WARNING,
    badgeClass: LEVEL_WARNING,
    labelKey: 'statusRollingBack',
  },
}

/** 运行期出现未声明的状态串时回落到「未激活 / 未知」档。 */
const UNKNOWN_LEVEL: StatusLevel = {
  dotClass: DOT_INACTIVE,
  badgeClass: LEVEL_INACTIVE,
  labelKey: 'statusUnknown',
}

interface AlgorithmStatusBadgeProps {
  status: AlgorithmStatus
  className?: string
}

export function AlgorithmStatusBadge({
  status,
  className = '',
}: AlgorithmStatusBadgeProps): React.JSX.Element {
  const { t } = useTranslation('algorithms')

  const { dotClass, badgeClass, labelKey } =
    STATUS_LEVEL[status] ?? UNKNOWN_LEVEL

  return (
    <Badge
      variant='outline'
      className={cn(
        'inline-flex items-center gap-1.5 px-2 py-0.5 text-[11px] font-medium',
        badgeClass,
        className
      )}
    >
      <span className={cn('h-1.5 w-1.5 rounded-full', dotClass)} />
      <span>{t(labelKey, status)}</span>
    </Badge>
  )
}
