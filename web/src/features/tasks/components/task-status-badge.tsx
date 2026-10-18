import { useTranslation } from 'react-i18next'
import { STATUS_DOT_CLASS, STATUS_LEVEL_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'

interface TaskStatusBadgeProps {
  enabled: boolean
  className?: string
}

export function TaskStatusBadge({
  enabled,
  className = '',
}: TaskStatusBadgeProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  const badgeClass = enabled
    ? STATUS_LEVEL_CLASS.active
    : STATUS_LEVEL_CLASS.inactive

  const dotClass = enabled ? STATUS_DOT_CLASS.active : STATUS_DOT_CLASS.inactive
  const label = enabled
    ? t('table.statusRunning', '运行中')
    : t('table.statusPaused', '已暂停')

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
