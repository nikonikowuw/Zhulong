import { useTranslation } from 'react-i18next'
import {
  STATUS_DOT_CLASS,
  STATUS_LEVEL_CLASS,
  type StatusLevel,
} from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { type AlgorithmStatus } from '@/features/algorithms/data/schema'

interface TaskStatusBadgeProps {
  enabled: boolean
  isChannelEnabled?: boolean
  algorithmStatus?: AlgorithmStatus
  className?: string
}

export function TaskStatusBadge({
  enabled,
  isChannelEnabled,
  algorithmStatus,
  className = '',
}: TaskStatusBadgeProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  let level: StatusLevel = 'inactive'
  let label =
    isChannelEnabled === false
      ? t('table.statusChannelPaused', '通道已暂停')
      : t('table.statusPaused', '已暂停')

  if (enabled && isChannelEnabled !== false) {
    if (algorithmStatus === 'DISABLED') {
      level = 'warning'
      label = t('table.statusStandby', '待命 (未激活)')
    } else if (
      algorithmStatus === 'CRASH_LOOP' ||
      algorithmStatus === 'CRASHED'
    ) {
      level = 'destructive'
      label = t('table.statusError', '异常')
    } else {
      level = 'active'
      label = t('table.statusRunning', '运行中')
    }
  }

  const badgeClass = STATUS_LEVEL_CLASS[level]
  const dotClass = STATUS_DOT_CLASS[level]

  return (
    <Badge
      variant='outline'
      className={cn(
        'inline-flex items-center gap-1.5 text-xs font-normal',
        badgeClass,
        className
      )}
    >
      <span className={cn('inline-block h-1.5 w-1.5 rounded-full', dotClass)} />
      {label}
    </Badge>
  )
}
