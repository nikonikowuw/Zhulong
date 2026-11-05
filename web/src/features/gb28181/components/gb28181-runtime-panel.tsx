import {
  AlertCircle,
  Activity,
  Clock,
  Globe,
  Layers,
  Network,
  RefreshCw,
  Server,
  ShieldAlert,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { statusLevelClass, statusTextClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  type Gb28181RuntimeInfo,
  type Gb28181RuntimeSnapshotStatus,
} from '../data/schema'
import { describeRuntimeDetail } from '../utils/error-message'
import {
  EMPTY_PLACEHOLDER,
  formatTimestamp,
  runtimeStateLevel,
} from '../utils/format'
import { Gb28181StatusBadge } from './gb28181-status-badge'

interface Gb28181RuntimePanelProps {
  runtimeInfo?: Gb28181RuntimeInfo
  snapshot?: Gb28181RuntimeSnapshotStatus
  isLoading: boolean
  isError: boolean
  errorMessage?: string
  isFetching: boolean
  onRetry: () => void
}

/**
 * 运行态面板：现场排障的第一入口。
 *
 * 采用 Cockpit 仪表盘布局：
 * - 核心业务容量（设备数 / 会话数 / 媒体端口）作为主指标横向展开；
 * - 协议防护与丢弃计数作为次级排障条紧凑收纳；
 * - 消除 4 列网格导致的 6 卡片空缺塌陷。
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

  const primaryMetrics = [
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
      // 单独以 text 节点渲染 snapshot.sessions.active，满足 exact 测试匹配
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
  ] as const

  const secondaryMetrics = [
    {
      key: 'exhausted',
      icon: AlertCircle,
      label: t('runtime.portExhausted'),
      value: snapshot
        ? String(snapshot.portPool.exhaustedHits)
        : EMPTY_PLACEHOLDER,
      isWarning: Boolean(snapshot && snapshot.portPool.exhaustedHits > 0),
    },
    {
      key: 'signalling',
      icon: ShieldAlert,
      label: t('runtime.ingressLimited'),
      value: snapshot
        ? t('runtime.ingressLimitedValue', {
            inbound: snapshot.ingress.inboundLimited,
            outbound: snapshot.ingress.outboundLimited,
          })
        : EMPTY_PLACEHOLDER,
      isWarning: Boolean(
        snapshot &&
        (snapshot.ingress.inboundLimited > 0 ||
          snapshot.ingress.outboundLimited > 0)
      ),
    },
    {
      key: 'signallingDrops',
      icon: ShieldAlert,
      label: t('runtime.ingressDrops'),
      value: snapshot
        ? t('runtime.ingressDropsValue', {
            oversized: snapshot.ingress.oversizedMessages,
            conns: snapshot.ingress.rejectedConns,
            active: snapshot.ingress.activeConns,
          })
        : EMPTY_PLACEHOLDER,
      isWarning: Boolean(
        snapshot &&
        (snapshot.ingress.oversizedMessages > 0 ||
          snapshot.ingress.rejectedConns > 0)
      ),
    },
  ] as const

  return (
    <section className='space-y-4 rounded-xl border border-border/80 bg-card/60 p-4 shadow-2xs backdrop-blur-xs'>
      {/* 标题栏与即时控制 */}
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div className='space-y-0.5'>
          <div className='flex items-center gap-2'>
            <h3 className='text-sm font-semibold tracking-tight'>
              {t('sections.runtime')}
            </h3>
            {state !== '' && (
              <Gb28181StatusBadge level={stateLevel} label={stateLabel} />
            )}
          </div>
          <p className='text-xs text-muted-foreground'>{t('runtime.desc')}</p>
        </div>

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

      {isError ? (
        <Alert className={statusLevelClass('destructive')}>
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
        <div className='space-y-3'>
          <div className='grid grid-cols-1 gap-3 sm:grid-cols-3'>
            {Array.from({ length: 3 }).map((_, index) => (
              <div
                key={`runtime-skeleton-primary-${index}`}
                className='rounded-lg border border-border/60 bg-muted/20 p-3.5'
              >
                <Skeleton className='h-3.5 w-24' />
                <Skeleton className='mt-2.5 h-6 w-32' />
              </div>
            ))}
          </div>
          <div className='grid grid-cols-1 gap-2.5 sm:grid-cols-3'>
            {Array.from({ length: 3 }).map((_, index) => (
              <div
                key={`runtime-skeleton-secondary-${index}`}
                className='rounded-lg border border-border/40 bg-muted/10 p-2.5'
              >
                <Skeleton className='h-3 w-20' />
                <Skeleton className='mt-1.5 h-4 w-28' />
              </div>
            ))}
          </div>
        </div>
      ) : (
        <div className='space-y-3'>
          {/* 核心容量与活跃度指标（3 等分网格，消除右侧塌陷） */}
          <div className='grid grid-cols-1 gap-3 sm:grid-cols-3'>
            {primaryMetrics.map((metric) => {
              const Icon = metric.icon
              return (
                <div
                  key={metric.key}
                  className='rounded-lg border border-border/70 bg-background/80 p-3.5 shadow-2xs transition-colors hover:border-border'
                >
                  <div className='flex items-center gap-2 text-xs font-medium text-muted-foreground'>
                    <div className='flex size-6 items-center justify-center rounded-md bg-muted/60 text-foreground/80'>
                      <Icon className='size-3.5' />
                    </div>
                    <span>{metric.label}</span>
                  </div>
                  <p className='mt-2.5 font-mono text-xl font-semibold tracking-tight text-foreground tabular-nums'>
                    {metric.value}
                  </p>
                </div>
              )
            })}
          </div>

          {/* 底层协议防护与排障计数（次级紧凑网格） */}
          <div className='grid grid-cols-1 gap-2.5 sm:grid-cols-3'>
            {secondaryMetrics.map((metric) => {
              const Icon = metric.icon
              return (
                <div
                  key={metric.key}
                  className={cn(
                    'rounded-lg border border-border/50 bg-muted/25 px-3 py-2 transition-colors',
                    metric.isWarning && statusLevelClass('warning')
                  )}
                >
                  <div className='flex items-center gap-1.5 text-xs text-muted-foreground'>
                    <Icon className='size-3.5 shrink-0' />
                    <span className='truncate'>{metric.label}</span>
                  </div>
                  <p className='mt-1 font-mono text-sm font-medium text-foreground tabular-nums'>
                    {metric.value}
                  </p>
                </div>
              )
            })}
          </div>
        </div>
      )}

      {/* 底部元信息状态条 */}
      <div className='flex flex-wrap items-center gap-x-6 gap-y-2 rounded-lg border border-border/40 bg-muted/20 px-3 py-2 text-xs text-muted-foreground'>
        <span className='inline-flex items-center gap-1.5'>
          <Globe className='size-3.5 text-muted-foreground/70' />
          <span>{t('runtime.advertisedIp')}</span>
          <span className='font-mono font-medium text-foreground'>
            {runtimeInfo?.advertisedIp || EMPTY_PLACEHOLDER}
          </span>
        </span>
        <span className='inline-flex items-center gap-1.5'>
          <Layers className='size-3.5 text-muted-foreground/70' />
          <span>{t('runtime.mediaCapacity')}</span>
          <span className='font-mono font-medium text-foreground'>
            {runtimeInfo
              ? t('runtime.mediaCapacityValue', {
                  count: runtimeInfo.mediaCapacity,
                })
              : EMPTY_PLACEHOLDER}
          </span>
        </span>
        <span className='ms-auto inline-flex items-center gap-1.5'>
          <Clock className='size-3.5 text-muted-foreground/70' />
          <span>{t('runtime.generatedAt')}</span>
          <span className='font-mono text-foreground'>
            {formatTimestamp(snapshot?.generatedAt, i18n.language)}
          </span>
        </span>
      </div>

      {detail !== '' && (
        <Alert className={statusLevelClass('warning')}>
          <AlertCircle className={cn('h-4 w-4', statusTextClass('warning'))} />
          <AlertTitle className='text-sm font-semibold'>
            {t('runtime.detailLabel')}
          </AlertTitle>
          <AlertDescription className='text-xs'>{detail}</AlertDescription>
        </Alert>
      )}
    </section>
  )
}
