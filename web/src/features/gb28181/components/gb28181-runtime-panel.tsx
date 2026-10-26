import { AlertCircle, Activity, Network, RefreshCw, Server } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { type Gb28181RuntimeInfo, type Gb28181Snapshot } from '../data/schema'
import { describeRuntimeDetail } from '../utils/error-message'
import {
  EMPTY_PLACEHOLDER,
  formatTimestamp,
  runtimeStateLevel,
} from '../utils/format'
import { Gb28181StatusBadge } from './gb28181-status-badge'

interface Gb28181RuntimePanelProps {
  runtimeInfo?: Gb28181RuntimeInfo
  snapshot?: Gb28181Snapshot
  isLoading: boolean
  isError: boolean
  errorMessage?: string
  isFetching: boolean
  onRetry: () => void
}

/**
 * 运行态面板：现场排障的第一入口。
 *
 * 这里展示的是「本进程实际在跑什么」，不是已保存的目标配置 ——
 * 两者不一致时由 `restartRequired` 提示，配置表单负责对比。
 */
export function Gb28181RuntimePanel({
  runtimeInfo,
  snapshot,
  isLoading,
  isError,
  errorMessage,
  isFetching,
  onRetry,
}: Gb28181RuntimePanelProps) {
  const { t, i18n } = useTranslation('gb28181')

  const state = runtimeInfo?.state ?? ''
  const stateLevel = runtimeStateLevel(state)
  const stateLabel = state
    ? t(`state.${state}`, { defaultValue: state })
    : EMPTY_PLACEHOLDER
  const detail = runtimeInfo ? describeRuntimeDetail(runtimeInfo.detail, t) : ''

  const metrics = [
    {
      key: 'devices',
      icon: Server,
      label: t('runtime.devicesOnline'),
      value: snapshot
        ? t('runtime.devicesValue', {
            online: snapshot.devices.online,
            total: snapshot.devices.total,
          })
        : EMPTY_PLACEHOLDER,
    },
    {
      key: 'sessions',
      icon: Activity,
      label: t('runtime.sessions'),
      value: snapshot ? String(snapshot.sessions.active) : EMPTY_PLACEHOLDER,
    },
    {
      key: 'ports',
      icon: Network,
      label: t('runtime.portPool'),
      value: snapshot
        ? t('runtime.portPoolValue', {
            used: snapshot.portPool.pairsUsed,
            total: snapshot.portPool.pairsTotal,
          })
        : EMPTY_PLACEHOLDER,
    },
    {
      key: 'exhausted',
      icon: AlertCircle,
      label: t('runtime.portExhausted'),
      value: snapshot
        ? String(snapshot.portPool.exhaustedHits)
        : EMPTY_PLACEHOLDER,
    },
  ] as const

  return (
    <section className='space-y-3'>
      <div className='flex flex-wrap items-start justify-between gap-2'>
        <div className='space-y-1'>
          <h3 className='text-sm font-semibold'>{t('sections.runtime')}</h3>
          <p className='text-xs text-muted-foreground'>{t('runtime.desc')}</p>
        </div>
        <div className='flex items-center gap-2'>
          {state !== '' && (
            <Gb28181StatusBadge level={stateLevel} label={stateLabel} />
          )}
          <Button
            variant='outline'
            size='sm'
            onClick={onRetry}
            disabled={isFetching}
            className='h-8 gap-1.5 text-xs'
          >
            <RefreshCw
              className={cn('h-3.5 w-3.5', isFetching && 'animate-spin')}
            />
            {t('actions.refresh')}
          </Button>
        </div>
      </div>

      {isError ? (
        <Alert className='border-destructive/20 bg-destructive/10 text-destructive dark:border-destructive/40 dark:bg-destructive/20'>
          <AlertCircle className='h-4 w-4' />
          <AlertTitle className='flex items-center justify-between gap-2 text-sm font-semibold'>
            {t('runtime.loadError')}
            <Button
              variant='outline'
              size='sm'
              onClick={onRetry}
              className='h-7 gap-1 text-xs'
            >
              <RefreshCw className='h-3 w-3' />
              {t('actions.retry')}
            </Button>
          </AlertTitle>
          <AlertDescription className='text-xs'>
            {errorMessage ?? t('errors.unknown')}
          </AlertDescription>
        </Alert>
      ) : isLoading ? (
        <div className='grid gap-2 sm:grid-cols-2 lg:grid-cols-4'>
          {Array.from({ length: 4 }).map((_, index) => (
            <div
              key={`runtime-skeleton-${index}`}
              className='rounded-lg border border-border bg-card p-3'
            >
              <Skeleton className='h-3 w-16' />
              <Skeleton className='mt-2 h-5 w-24' />
            </div>
          ))}
        </div>
      ) : (
        <div className='grid gap-2 sm:grid-cols-2 lg:grid-cols-4'>
          {metrics.map((metric) => {
            const Icon = metric.icon
            return (
              <div
                key={metric.key}
                className='rounded-lg border border-border bg-card p-3'
              >
                <div className='flex items-center gap-1.5 text-[11px] text-muted-foreground'>
                  <Icon className='h-3.5 w-3.5' />
                  <span>{metric.label}</span>
                </div>
                <p className='mt-1 font-mono text-lg leading-tight font-semibold'>
                  {metric.value}
                </p>
              </div>
            )
          })}
        </div>
      )}

      <div className='flex flex-wrap gap-x-6 gap-y-1 text-xs text-muted-foreground'>
        <span className='inline-flex items-center gap-1'>
          {t('runtime.advertisedIp')}
          <span className='font-mono text-foreground'>
            {runtimeInfo?.advertisedIp || EMPTY_PLACEHOLDER}
          </span>
        </span>
        <span className='inline-flex items-center gap-1'>
          {t('runtime.mediaCapacity')}
          <span className='font-mono text-foreground'>
            {runtimeInfo
              ? t('runtime.mediaCapacityValue', {
                  count: runtimeInfo.mediaCapacity,
                })
              : EMPTY_PLACEHOLDER}
          </span>
        </span>
        <span className='inline-flex items-center gap-1'>
          {t('runtime.generatedAt')}
          <span className='font-mono text-foreground'>
            {formatTimestamp(snapshot?.generatedAt, i18n.language)}
          </span>
        </span>
      </div>

      {detail !== '' && (
        <Alert className='border-amber-200 bg-amber-100/30 text-amber-900 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-200'>
          <AlertCircle className='h-4 w-4' />
          <AlertTitle className='text-sm font-semibold'>
            {t('runtime.detailLabel')}
          </AlertTitle>
          <AlertDescription className='text-xs'>{detail}</AlertDescription>
        </Alert>
      )}
    </section>
  )
}
