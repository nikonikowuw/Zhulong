import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { type AlgorithmStatus } from '../data/schema'

interface AlgorithmStatusBadgeProps {
  status: AlgorithmStatus
  className?: string
}

export function AlgorithmStatusBadge({
  status,
  className = '',
}: AlgorithmStatusBadgeProps): React.JSX.Element {
  const { t } = useTranslation('algorithms')

  const config: Record<
    AlgorithmStatus,
    { dotClass: string; badgeClass: string; labelKey: string }
  > = {
    ACTIVE: {
      dotClass: 'bg-emerald-500 animate-pulse',
      badgeClass:
        'border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
      labelKey: 'statusActive',
    },
    RUNNING: {
      dotClass: 'bg-emerald-500 animate-pulse',
      badgeClass:
        'border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
      labelKey: 'statusRunning',
    },
    STARTING: {
      dotClass: 'bg-sky-500 animate-ping',
      badgeClass:
        'border-sky-500/30 bg-sky-500/10 text-sky-600 dark:text-sky-400',
      labelKey: 'statusStarting',
    },
    DISABLED: {
      dotClass: 'bg-zinc-400',
      badgeClass:
        'border-zinc-500/30 bg-zinc-500/10 text-zinc-600 dark:text-zinc-400',
      labelKey: 'statusDisabled',
    },
    CRASHED: {
      dotClass: 'bg-rose-500',
      badgeClass:
        'border-rose-500/30 bg-rose-500/10 text-rose-600 dark:text-rose-400',
      labelKey: 'statusCrashed',
    },
    CRASH_LOOP: {
      dotClass: 'bg-rose-500 animate-pulse',
      badgeClass:
        'border-rose-500/30 bg-rose-500/10 text-rose-600 dark:text-rose-400',
      labelKey: 'statusCrashLoop',
    },
    ROLLING_BACK: {
      dotClass: 'bg-amber-500 animate-bounce',
      badgeClass:
        'border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400',
      labelKey: 'statusRollingBack',
    },
  }

  const { dotClass, badgeClass, labelKey } = config[status] ?? {
    dotClass: 'bg-zinc-400',
    badgeClass: 'border-zinc-500/30 text-zinc-500',
    labelKey: 'statusUnknown',
  }

  return (
    <Badge
      variant='outline'
      className={`inline-flex items-center gap-1.5 px-2 py-0.5 text-[11px] font-medium ${badgeClass} ${className}`}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${dotClass}`} />
      <span>{t(labelKey, status)}</span>
    </Badge>
  )
}
