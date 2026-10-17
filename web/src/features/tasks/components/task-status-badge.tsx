import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

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
    ? 'border-teal-200 bg-teal-100/30 text-teal-900 dark:border-teal-800 dark:bg-teal-950/30 dark:text-teal-200'
    : 'border-neutral-300 bg-neutral-300/40 text-muted-foreground dark:border-neutral-700 dark:bg-neutral-800/40'

  const dotClass = enabled ? 'bg-emerald-500' : 'bg-muted-foreground/60'
  const label = enabled ? t('table.statusRunning', '运行中') : t('table.statusPaused', '已暂停')

  return (
    <Badge
      variant='outline'
      className={cn('inline-flex items-center gap-1.5 text-xs font-normal', badgeClass, className)}
    >
      <span className={cn('inline-block h-1.5 w-1.5 rounded-full', dotClass)} />
      {label}
    </Badge>
  )
}
