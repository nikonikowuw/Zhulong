import { DotsHorizontalIcon } from '@radix-ui/react-icons'
import { Copy, Eye } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { type AuditLog } from '../data/schema'
import { useAuditContext } from './AuditProvider'

export function StatusBadge({ status }: { status: string }) {
  const { t } = useTranslation('audit')
  const isSuccess = status === 'success'
  const isFailed = status === 'failed'

  if (isSuccess) {
    return (
      <Badge
        variant='outline'
        className='gap-1.5 border-teal-200 bg-teal-100/30 text-xs font-normal text-teal-900 dark:border-teal-800 dark:bg-teal-950/30 dark:text-teal-200'
      >
        <span className='h-1.5 w-1.5 rounded-full bg-emerald-500' />
        {t('status.success')}
      </Badge>
    )
  }

  if (isFailed) {
    return (
      <Badge
        variant='outline'
        className='gap-1.5 border-destructive/20 bg-destructive/10 text-xs font-normal text-destructive dark:border-destructive/40 dark:bg-destructive/20 dark:text-destructive'
      >
        <span className='h-1.5 w-1.5 rounded-full bg-destructive' />
        {t('status.failed')}
      </Badge>
    )
  }

  return (
    <Badge
      variant='outline'
      className='gap-1.5 border-neutral-300 bg-neutral-300/40 text-xs font-normal text-muted-foreground dark:border-neutral-700 dark:bg-neutral-800/40'
    >
      <span className='h-1.5 w-1.5 rounded-full bg-muted-foreground/60' />
      {status || '-'}
    </Badge>
  )
}

export function ActionCell({ log }: { log: AuditLog }) {
  const { openDetail } = useAuditContext()
  const { t } = useTranslation('audit')

  return (
    <div className='flex items-center justify-end'>
      <DropdownMenu modal={false}>
        <DropdownMenuTrigger asChild>
          <Button
            variant='ghost'
            className='h-8 w-8 p-0 text-muted-foreground hover:text-foreground data-[state=open]:bg-muted'
            onClick={(e) => e.stopPropagation()}
          >
            <DotsHorizontalIcon className='h-4 w-4' />
            <span className='sr-only'>{t('actions.openMenu')}</span>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align='end' className='w-40'>
          <DropdownMenuItem
            onClick={(e) => {
              e.stopPropagation()
              openDetail(log)
            }}
          >
            <Eye className='me-2 h-4 w-4' />
            {t('actions.viewDetail')}
          </DropdownMenuItem>
          {log.target && (
            <DropdownMenuItem
              onClick={async (e) => {
                e.stopPropagation()
                try {
                  await navigator.clipboard.writeText(log.target)
                  toast.success(t('toast.copyTargetSuccess'))
                } catch {
                  toast.error(t('toast.copyFailed'))
                }
              }}
            >
              <Copy className='me-2 h-4 w-4' />
              {t('actions.copyTarget')}
            </DropdownMenuItem>
          )}
          {log.ip && (
            <DropdownMenuItem
              onClick={async (e) => {
                e.stopPropagation()
                try {
                  await navigator.clipboard.writeText(log.ip)
                  toast.success(t('toast.copyIpSuccess'))
                } catch {
                  toast.error(t('toast.copyFailed'))
                }
              }}
            >
              <Copy className='me-2 h-4 w-4' />
              {t('actions.copyIp')}
            </DropdownMenuItem>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}

export function FormattedTimeCell({ isoTime }: { isoTime: string }) {
  const { i18n } = useTranslation()

  let formatted = isoTime
  try {
    const d = new Date(isoTime)
    if (!isNaN(d.getTime())) {
      formatted = new Intl.DateTimeFormat(i18n.language || 'zh-Hans', {
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false,
      }).format(d)
    }
  } catch {
    // Keep raw string fallback
  }

  return (
    <span className='font-mono text-xs whitespace-nowrap text-foreground/80'>
      {formatted}
    </span>
  )
}

export function ActionNameCell({ action }: { action: string }) {
  const { t } = useTranslation('audit')
  const label = t(`actionNames.${action}`, { defaultValue: action })

  return (
    <div className='flex flex-col'>
      <span className='text-xs font-medium text-foreground'>{label}</span>
      <span className='hidden font-mono text-[11px] text-muted-foreground/70 lg:inline'>
        {action}
      </span>
    </div>
  )
}
