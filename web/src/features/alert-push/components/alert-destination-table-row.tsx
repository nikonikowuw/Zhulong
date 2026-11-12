import React from 'react'
import {
  AlertTriangle,
  BellRing,
  CheckCircle2,
  Clock,
  Globe,
  HeartPulse,
  Layers,
  MoreHorizontal,
  Pencil,
  Play,
  Radio,
  Trash2,
  XCircle,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Switch } from '@/components/ui/switch'
import { TableCell, TableRow } from '@/components/ui/table'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import type { AlertDestination, DestinationStatus } from '../types'

interface DestinationTableRowProps {
  dest: AlertDestination
  onEdit: (dest: AlertDestination) => void
  onDelete: (dest: AlertDestination) => void
  onToggle: (dest: AlertDestination, enabled: boolean) => void
  onTest: (dest: AlertDestination) => void
}

function formatSafeTime(dateStr?: string): string {
  if (!dateStr) return '-'
  const d = new Date(dateStr)
  return isNaN(d.getTime()) ? '-' : d.toLocaleTimeString()
}

function formatSafeDateTime(dateStr?: string): string {
  if (!dateStr) return '-'
  const d = new Date(dateStr)
  return isNaN(d.getTime()) ? '-' : d.toLocaleString()
}

export function DestinationStatusBadge({
  status,
  enabled,
}: {
  status: DestinationStatus
  enabled: boolean
}) {
  const { t } = useTranslation('alertPush')

  if (!enabled) {
    return (
      <Badge variant='secondary' className='gap-1 opacity-70'>
        <XCircle className='size-3' />
        {t('states.disabled')}
      </Badge>
    )
  }

  switch (status.state) {
    case 'running':
      return (
        <Badge
          variant='outline'
          className='gap-1 border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
        >
          <span className='relative flex size-2'>
            <span className='absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75' />
            <span className='relative inline-flex size-2 rounded-full bg-emerald-500' />
          </span>
          {t('states.running')}
        </Badge>
      )
    case 'degraded':
      return (
        <Tooltip>
          <TooltipTrigger asChild>
            <Badge variant='destructive' className='cursor-help gap-1'>
              <AlertTriangle className='size-3' />
              {t('states.degraded')}
              {status.consecutiveFailures > 0
                ? ` (${status.consecutiveFailures})`
                : ''}
            </Badge>
          </TooltipTrigger>
          <TooltipContent>
            <p className='text-xs'>
              {status.lastFailureReason
                ? t(`failureReasons.${status.lastFailureReason}`, {
                    defaultValue: status.lastFailureReason,
                  })
                : ''}
            </p>
            {status.lastFailureAt ? (
              <p className='text-xs opacity-75'>
                {formatSafeDateTime(status.lastFailureAt)}
              </p>
            ) : null}
          </TooltipContent>
        </Tooltip>
      )
    case 'idle':
    default:
      return (
        <Badge
          variant='outline'
          className='gap-1 border-blue-500/30 bg-blue-500/10 text-blue-600 dark:text-blue-400'
        >
          <CheckCircle2 className='size-3' />
          {t('states.idle')}
        </Badge>
      )
  }
}

export function DestinationImageModeBadge({ mode }: { mode: string }) {
  switch (mode) {
    case 'url':
      return <Badge variant='outline'>URL</Badge>
    case 'multipart':
      return <Badge variant='secondary'>Multipart</Badge>
    case 'base64':
      return <Badge variant='secondary'>Base64</Badge>
    default:
      return <Badge variant='outline'>{mode}</Badge>
  }
}

export const AlertDestinationTableRow = React.memo(
  function AlertDestinationTableRow({
    dest,
    onEdit,
    onDelete,
    onToggle,
    onTest,
  }: DestinationTableRowProps) {
    const { t } = useTranslation('alertPush')
    const hbInterval = dest.config.heartbeatIntervalSeconds ?? 60

    return (
      <TableRow>
        <TableCell className='font-medium'>
          <div className='flex flex-col gap-1.5'>
            <div className='flex flex-wrap items-center gap-1.5'>
              <span>{dest.name}</span>
              {dest.eventsEnabled ? (
                <Badge
                  variant='outline'
                  className='h-5 gap-1 px-1.5 text-[10px] font-normal'
                >
                  <BellRing className='size-2.5 text-primary' />
                  {t('capabilities.eventsBadge')}
                </Badge>
              ) : null}
              {dest.heartbeatEnabled ? (
                <Badge
                  variant='outline'
                  className='h-5 gap-1 px-1.5 text-[10px] font-normal'
                >
                  <HeartPulse className='size-2.5 text-emerald-500' />
                  {t('capabilities.heartbeatBadge', { interval: hbInterval })}
                </Badge>
              ) : null}
              {dest.eventsEnabled ? (
                dest.filter?.eventTypes && dest.filter.eventTypes.length > 0 ? (
                  <div className='flex flex-wrap gap-1'>
                    {dest.filter.eventTypes.map((et) => (
                      <span
                        key={et}
                        className='rounded bg-muted px-1 py-0.5 text-[10px] text-muted-foreground'
                      >
                        {t(`eventTypes.${et}`)}
                      </span>
                    ))}
                  </div>
                ) : (
                  <span className='rounded bg-muted px-1 py-0.5 text-[10px] text-muted-foreground'>
                    {t('eventTypes.all')}
                  </span>
                )
              ) : null}
            </div>
            <span className='max-w-[240px] truncate font-mono text-xs text-muted-foreground'>
              {dest.channelType === 'webhook'
                ? dest.config.url
                : dest.config.broker}
            </span>
          </div>
        </TableCell>

        <TableCell>
          <div className='flex items-center gap-1.5 text-sm'>
            {dest.channelType === 'webhook' ? (
              <Globe className='size-4 text-sky-500' />
            ) : (
              <Radio className='size-4 text-violet-500' />
            )}
            <span>{t(`channelTypes.${dest.channelType}`)}</span>
          </div>
        </TableCell>

        <TableCell>
          {dest.eventsEnabled ? (
            <DestinationImageModeBadge mode={dest.imageMode} />
          ) : (
            <span className='text-xs text-muted-foreground'>—</span>
          )}
        </TableCell>

        <TableCell>
          <Switch
            checked={dest.enabled}
            onCheckedChange={(checked) => onToggle(dest, checked)}
          />
        </TableCell>

        <TableCell>
          <DestinationStatusBadge status={dest.status} enabled={dest.enabled} />
        </TableCell>

        <TableCell>
          {dest.eventsEnabled ? (
            <div className='flex items-center gap-1 text-xs text-muted-foreground'>
              <Layers className='size-3.5' />
              <span>
                {dest.status.queueLength} / {dest.status.queueCapacity}
              </span>
            </div>
          ) : (
            <span className='text-xs text-muted-foreground'>—</span>
          )}
        </TableCell>

        <TableCell>
          <div className='flex items-center gap-1 text-xs text-muted-foreground'>
            <Clock className='size-3.5' />
            <span>{formatSafeTime(dest.status.lastAttemptAt)}</span>
          </div>
        </TableCell>

        <TableCell className='text-right'>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant='ghost' size='icon' className='size-8'>
                <MoreHorizontal className='size-4' />
                <span className='sr-only'>{t('fields.actions')}</span>
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align='end'>
              <DropdownMenuItem onClick={() => onTest(dest)} className='gap-2'>
                <Play className='size-4' />
                {t('test')}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => onEdit(dest)} className='gap-2'>
                <Pencil className='size-4' />
                {t('edit')}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                onClick={() => onDelete(dest)}
                className='gap-2 text-destructive focus:text-destructive'
              >
                <Trash2 className='size-4' />
                {t('delete')}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </TableCell>
      </TableRow>
    )
  }
)
