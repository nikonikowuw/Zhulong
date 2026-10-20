import { DotsHorizontalIcon } from '@radix-ui/react-icons'
import { Copy, Eye } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { copyToClipboard } from '@/lib/clipboard'
import { STATUS_DOT_CLASS, STATUS_LEVEL_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
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
        className={cn('gap-1.5 text-xs font-normal', STATUS_LEVEL_CLASS.active)}
      >
        <span
          className={cn('h-1.5 w-1.5 rounded-full', STATUS_DOT_CLASS.active)}
        />
        {t('status.success')}
      </Badge>
    )
  }

  if (isFailed) {
    return (
      <Badge
        variant='outline'
        className={cn(
          'gap-1.5 text-xs font-normal',
          STATUS_LEVEL_CLASS.destructive
        )}
      >
        <span
          className={cn(
            'h-1.5 w-1.5 rounded-full',
            STATUS_DOT_CLASS.destructive
          )}
        />
        {t('status.failed')}
      </Badge>
    )
  }

  return (
    <Badge
      variant='outline'
      className={cn('gap-1.5 text-xs font-normal', STATUS_LEVEL_CLASS.inactive)}
    >
      <span
        className={cn('h-1.5 w-1.5 rounded-full', STATUS_DOT_CLASS.inactive)}
      />
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
                if (await copyToClipboard(log.target)) {
                  toast.success(t('toast.copyTargetSuccess'))
                } else {
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
                if (await copyToClipboard(log.ip)) {
                  toast.success(t('toast.copyIpSuccess'))
                } else {
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
