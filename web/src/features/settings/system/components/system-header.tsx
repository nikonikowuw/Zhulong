import { RefreshCw, Server, Cpu, Clock, Activity } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { type HostInfo } from '../types'
import { formatUptime } from '../utils/format'

interface SystemHeaderProps {
  host: HostInfo
  isRefetching: boolean
  onRefresh: () => void
}

export function SystemHeader({
  host,
  isRefetching,
  onRefresh,
}: SystemHeaderProps) {
  const { t } = useTranslation('system')

  return (
    <div className='flex flex-col gap-4 rounded-xl border bg-card/60 p-4 shadow-xs backdrop-blur-xs md:flex-row md:items-center md:justify-between'>
      <div className='flex flex-wrap items-center gap-x-6 gap-y-2 text-sm'>
        <div className='flex items-center gap-2 font-medium'>
          <Server className='size-4 text-primary' />
          <span>{host.hostname || 'Edge-Host'}</span>
        </div>

        <div className='flex items-center gap-1.5 text-muted-foreground'>
          <Cpu className='size-3.5' />
          <span>
            {host.os}/{host.arch}
          </span>
          <Badge variant='outline' className='px-1.5 py-0 font-mono text-xs'>
            {host.kernelVersion || 'Linux'}
          </Badge>
        </div>

        <div className='flex items-center gap-1.5 text-muted-foreground'>
          <Clock className='size-3.5' />
          <span>{t('header.uptime', { defaultValue: 'Uptime' })}:</span>
          <span className='font-mono font-medium text-foreground'>
            {formatUptime(host.uptimeSeconds)}
          </span>
        </div>

        <div className='flex items-center gap-1.5 text-muted-foreground'>
          <Activity className='size-3.5' />
          <span>{t('header.loadAvg', { defaultValue: 'Load Avg' })}:</span>
          <span className='font-mono text-xs'>
            {host.loadAvg[0]?.toFixed(2)} / {host.loadAvg[1]?.toFixed(2)} /{' '}
            {host.loadAvg[2]?.toFixed(2)}
          </span>
        </div>
      </div>

      <div className='flex items-center gap-3'>
        <div className='flex items-center gap-1.5 text-xs text-muted-foreground'>
          <span className='relative flex size-2'>
            <span className='absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75' />
            <span className='relative inline-flex size-2 rounded-full bg-emerald-500' />
          </span>
          <span>
            {t('header.autoRefreshing', { defaultValue: 'Live (2.5s)' })}
          </span>
        </div>

        <Button
          variant='outline'
          size='sm'
          onClick={onRefresh}
          disabled={isRefetching}
          className='h-8 gap-1.5'
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
