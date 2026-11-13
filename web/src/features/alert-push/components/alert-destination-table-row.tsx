import React from 'react'
import {
  BellRing,
  Clock,
  Globe,
  HeartPulse,
  Layers,
  MoreHorizontal,
  Pencil,
  Play,
  Radio,
  Trash2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { categoryTextClass } from '@/lib/category-palette'
import {
  statusDotClass,
  statusLevelClass,
  type StatusLevel,
} from '@/lib/status-palette'
import { cn } from '@/lib/utils'
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

const timeFormatters = new Map<string, Intl.DateTimeFormat>()
const dateTimeFormatters = new Map<string, Intl.DateTimeFormat>()

function getTimeFormatter(language: string): Intl.DateTimeFormat {
  const key = language || 'default'
  let formatter = timeFormatters.get(key)
  if (!formatter) {
    formatter = new Intl.DateTimeFormat(language || undefined, {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    })
    timeFormatters.set(key, formatter)
  }
  return formatter
}

function getDateTimeFormatter(language: string): Intl.DateTimeFormat {
  const key = language || 'default'
  let formatter = dateTimeFormatters.get(key)
  if (!formatter) {
    formatter = new Intl.DateTimeFormat(language || undefined, {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    })
    dateTimeFormatters.set(key, formatter)
  }
  return formatter
}

function formatSafeTime(dateStr: string | undefined, language: string): string {
  if (!dateStr) return '-'
  const d = new Date(dateStr)
  return isNaN(d.getTime()) ? '-' : getTimeFormatter(language).format(d)
}

function formatSafeDateTime(
  dateStr: string | undefined,
  language: string
): string {
  if (!dateStr) return '-'
  const d = new Date(dateStr)
  return isNaN(d.getTime()) ? '-' : getDateTimeFormatter(language).format(d)
}

const DESTINATION_STATUS_LEVEL: Record<
  DestinationStatus['state'],
  StatusLevel
> = {
  running: 'active',
  degraded: 'destructive',
  idle: 'inactive',
  disabled: 'inactive',
}

export function DestinationStatusBadge({
  status,
  enabled,
}: {
  status: DestinationStatus
  enabled: boolean
}) {
  const { t, i18n } = useTranslation('alertPush')

  if (!enabled || status.state === 'disabled') {
    return (
      <Badge
        variant='outline'
        className={cn(
          'gap-1.5 text-xs font-normal opacity-80',
          statusLevelClass('inactive')
        )}
      >
        <span
          className={cn('size-1.5 rounded-full', statusDotClass('inactive'))}
        />
        {t('states.disabled')}
      </Badge>
    )
  }

  const level = DESTINATION_STATUS_LEVEL[status.state]

  if (status.state === 'running') {
    return (
      <Badge
        variant='outline'
        className={cn('gap-1.5 text-xs font-normal', statusLevelClass(level))}
      >
        <span className='relative flex size-1.5'>
          <span
            className={cn(
              'absolute inline-flex h-full w-full animate-ping rounded-full opacity-75',
              statusDotClass(level)
            )}
          />
          <span
            className={cn(
              'relative inline-flex size-1.5 rounded-full',
              statusDotClass(level)
            )}
          />
        </span>
        {t('states.running')}
      </Badge>
    )
  }

  if (status.state === 'degraded') {
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <Badge
            variant='outline'
            className={cn(
              'cursor-help gap-1.5 text-xs font-normal',
              statusLevelClass(level)
            )}
          >
            <span
              className={cn('size-1.5 rounded-full', statusDotClass(level))}
            />
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
              {formatSafeDateTime(status.lastFailureAt, i18n.language)}
            </p>
          ) : null}
        </TooltipContent>
      </Tooltip>
    )
  }

  return (
    <Badge
      variant='outline'
      className={cn('gap-1.5 text-xs font-normal', statusLevelClass(level))}
    >
      <span className={cn('size-1.5 rounded-full', statusDotClass(level))} />
      {t('states.idle')}
    </Badge>
  )
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
    const { t, i18n } = useTranslation('alertPush')
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
                  <HeartPulse className='size-2.5 text-primary' />
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
              <Globe className={cn('size-4', categoryTextClass('blue'))} />
            ) : (
              <Radio className={cn('size-4', categoryTextClass('violet'))} />
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
            aria-label={dest.name}
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
            <span>
              {formatSafeTime(dest.status.lastAttemptAt, i18n.language)}
            </span>
          </div>
        </TableCell>

        <TableCell className='text-end'>
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
