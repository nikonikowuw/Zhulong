import {
  Activity,
  Clock,
  Cpu,
  Pause,
  Play,
  RefreshCw,
  Server,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { type HostInfo } from '../types'
import { formatUptime } from '../utils/format'

interface SystemHeaderProps {
  host: HostInfo
  isRefetching: boolean
  isPaused: boolean
  onTogglePause: () => void
  onRefresh: () => void
}

export function SystemHeader({
  host,
  isRefetching,
  isPaused,
  onTogglePause,
  onRefresh,
}: SystemHeaderProps) {
  const { t } = useTranslation('system')

  return (
    <div className='flex flex-col gap-4 rounded-xl border border-border/80 bg-card/75 p-4 shadow-xs backdrop-blur-md transition-all md:flex-row md:items-center md:justify-between'>
      <div className='flex flex-wrap items-center gap-x-5 gap-y-2.5 text-sm'>
        {/* Node Name & Health Dot */}
        <div className='flex shrink-0 items-center gap-2.5'>
          <div className='flex size-8 shrink-0 items-center justify-center rounded-lg border border-primary/20 bg-primary/10 text-primary shadow-2xs'>
            <Server className='size-4' />
          </div>
          <div className='flex items-center gap-2'>
            <span className='font-semibold tracking-tight text-foreground'>
              {host.hostname || 'Edge-Host'}
            </span>
            <span
              className='relative flex size-2'
              title='Node Active'
              aria-hidden='true'
            >
              <span className='absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-60' />
              <span className='relative inline-flex size-2 rounded-full bg-emerald-500' />
            </span>
          </div>
        </div>

        {/* OS & Kernel Badge */}
        <div className='flex shrink-0 items-center gap-1.5 rounded-md border border-border/60 bg-muted/40 px-2 py-1 text-xs text-muted-foreground'>
          <Cpu className='size-3.5 text-sky-500' />
          <span className='font-mono font-medium text-foreground'>
            {host.os}/{host.arch}
          </span>
          <Badge
            variant='outline'
            className='border-border/80 bg-background/80 px-1.5 py-0 font-mono text-[11px]'
          >
            {host.kernelVersion || 'Linux'}
          </Badge>
        </div>

        {/* Uptime */}
        <div className='flex shrink-0 items-center gap-1.5 rounded-md border border-border/60 bg-muted/40 px-2 py-1 text-xs text-muted-foreground'>
          <Clock className='size-3.5 text-amber-500' />
          <span>{t('header.uptime', { defaultValue: 'Uptime' })}:</span>
          <span className='font-mono font-medium text-foreground'>
            {formatUptime(host.uptimeSeconds)}
          </span>
        </div>

        {/* Load Average */}
        <div className='flex shrink-0 items-center gap-1.5 rounded-md border border-border/60 bg-muted/40 px-2 py-1 text-xs text-muted-foreground'>
          <Activity className='size-3.5 text-emerald-500' />
          <span className='whitespace-nowrap'>
            {t('header.loadAvg', { defaultValue: 'Load Avg' })}:
          </span>
          <span className='font-mono font-medium text-foreground'>
            {host.loadAvg[0]?.toFixed(2)} / {host.loadAvg[1]?.toFixed(2)} /{' '}
            {host.loadAvg[2]?.toFixed(2)}
          </span>
        </div>
      </div>

      {/* Control Buttons */}
      <div className='flex shrink-0 items-center gap-2 self-end md:self-auto'>
        <Button
          variant='ghost'
          size='sm'
          onClick={onTogglePause}
          className={`h-8 gap-1.5 rounded-lg border px-2.5 text-xs font-medium transition-all ${
            isPaused
              ? 'border-amber-500/30 bg-amber-500/10 text-amber-600 hover:bg-amber-500/20 dark:text-amber-400'
              : 'border-border/60 bg-muted/40 text-muted-foreground hover:bg-muted/70 hover:text-foreground'
          }`}
          title={
            isPaused
              ? t('header.resume', { defaultValue: 'Resume live telemetry' })
              : t('header.pause', { defaultValue: 'Pause live telemetry' })
          }
          aria-label={
            isPaused
              ? t('header.resume', { defaultValue: 'Resume live telemetry' })
              : t('header.pause', { defaultValue: 'Pause live telemetry' })
          }
        >
          {isPaused ? (
            <>
              <Play className='size-3 fill-amber-500 text-amber-500' />
              <span className='font-medium text-amber-600 dark:text-amber-400'>
                {t('header.paused', { defaultValue: 'Paused' })}
              </span>
            </>
          ) : (
            <>
              <span className='relative flex size-2' aria-hidden='true'>
                <span className='absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75' />
                <span className='relative inline-flex size-2 rounded-full bg-emerald-500' />
              </span>
              <span className='text-foreground'>
                {t('header.autoRefreshing', { defaultValue: 'Live (2.5s)' })}
              </span>
              <Pause className='size-3 opacity-60' />
            </>
          )}
        </Button>

        <Button
          variant='outline'
          size='sm'
          onClick={onRefresh}
          disabled={isRefetching}
          className='h-8 gap-1.5 rounded-lg border-border/80 shadow-2xs hover:bg-muted/60'
        >
          <RefreshCw
            className={`size-3.5 ${isRefetching ? 'animate-spin' : ''}`}
          />
          <span>{t('header.refresh', { defaultValue: 'Refresh' })}</span>
        </Button>
      </div>
    </div>
  )
}
