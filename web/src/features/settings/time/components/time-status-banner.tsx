import {
  AlertTriangle,
  RefreshCw,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useLiveClock } from '../hooks/use-live-clock'
import { type SystemTimeStatus } from '../types'

interface TimeStatusBannerProps {
  status?: SystemTimeStatus
  isSyncing: boolean
  onSyncNow: () => void
}

function renderSyncBadge(
  state: string,
  t: (key: string, opts?: { defaultValue?: string }) => string
) {
  switch (state) {
    case 'synchronized':
      return (
        <Badge
          variant='outline'
          className='gap-1.5 text-xs font-normal border-teal-200 bg-teal-100/30 text-teal-900 dark:border-teal-800 dark:bg-teal-950/30 dark:text-teal-200'
        >
          <span className='inline-block h-1.5 w-1.5 rounded-full bg-emerald-500' />
          {t('dashboard.stateSynchronized', { defaultValue: '已同步' })}
        </Badge>
      )
    case 'syncing':
      return (
        <Badge
          variant='outline'
          className='gap-1.5 text-xs font-normal border-amber-200 bg-amber-100/30 text-amber-900 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-200'
        >
          <span className='inline-block h-1.5 w-1.5 rounded-full bg-amber-500 animate-pulse' />
          {t('dashboard.stateSyncing', { defaultValue: '同步中' })}
        </Badge>
      )
    case 'panic_review':
      return (
        <Badge
          variant='outline'
          className='gap-1.5 text-xs font-normal border-amber-200 bg-amber-100/30 text-amber-900 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-200'
        >
          <span className='inline-block h-1.5 w-1.5 rounded-full bg-amber-500' />
          {t('dashboard.statePanicReview', { defaultValue: '复核中' })}
        </Badge>
      )
    case 'failed':
      return (
        <Badge
          variant='outline'
          className='gap-1.5 text-xs font-normal border-destructive/20 bg-destructive/10 text-destructive dark:border-destructive/40 dark:bg-destructive/20 dark:text-destructive'
        >
          <span className='inline-block h-1.5 w-1.5 rounded-full bg-destructive' />
          {t('dashboard.stateFailed', { defaultValue: '同步失败' })}
        </Badge>
      )
    default:
      return (
        <Badge
          variant='outline'
          className='gap-1.5 text-xs font-normal border-neutral-300 bg-neutral-300/40 text-muted-foreground dark:border-neutral-700 dark:bg-neutral-800/40'
        >
          <span className='inline-block h-1.5 w-1.5 rounded-full bg-muted-foreground/60' />
          {t('dashboard.stateUnsynced', { defaultValue: '未同步' })}
        </Badge>
      )
  }
}

function renderRtcBadge(
  state: string,
  t: (key: string, opts?: { defaultValue?: string }) => string
) {
  switch (state) {
    case 'normal':
      return (
        <Badge
          variant='outline'
          className='gap-1.5 text-xs font-normal border-teal-200 bg-teal-100/30 text-teal-900 dark:border-teal-800 dark:bg-teal-950/30 dark:text-teal-200'
        >
          <span className='inline-block h-1.5 w-1.5 rounded-full bg-emerald-500' />
          {t('dashboard.rtcNormal', { defaultValue: 'RTC 就绪' })}
        </Badge>
      )
    case 'error':
      return (
        <Badge
          variant='outline'
          className='gap-1.5 text-xs font-normal border-destructive/20 bg-destructive/10 text-destructive dark:border-destructive/40 dark:bg-destructive/20 dark:text-destructive'
        >
          <span className='inline-block h-1.5 w-1.5 rounded-full bg-destructive' />
          {t('dashboard.rtcError', { defaultValue: 'RTC 异常' })}
        </Badge>
      )
    default:
      return (
        <Badge
          variant='outline'
          className='gap-1.5 text-xs font-normal border-neutral-300 bg-neutral-300/40 text-muted-foreground dark:border-neutral-700 dark:bg-neutral-800/40'
        >
          <span className='inline-block h-1.5 w-1.5 rounded-full bg-muted-foreground/60' />
          {t('dashboard.rtcMissing', { defaultValue: '无 RTC 硬件' })}
        </Badge>
      )
  }
}

export function TimeStatusBanner({
  status,
  isSyncing,
  onSyncNow,
}: TimeStatusBannerProps) {
  const { t } = useTranslation('time')
  const { timeString, dateString } = useLiveClock({
    serverTimeStr: status?.currentTime,
    timezone: status?.timezone,
  })

  const syncState = status?.syncStatus.state ?? 'unsynced'
  const rtcState = status?.rtcStatus ?? 'missing'

  return (
    <div className='space-y-3'>
      {/* 权限受限警告 */}
      {status && !status.hasPermission && (
        <Alert variant='destructive' className='py-2 px-3 text-xs'>
          <AlertTriangle className='h-4 w-4' />
          <AlertTitle className='font-semibold text-xs'>
            {t('dashboard.permissionWarningTitle', {
              defaultValue: '系统权限受限',
            })}
          </AlertTitle>
          <AlertDescription className='text-xs'>
            {t('dashboard.permissionWarningDesc', {
              defaultValue:
                '当前进程缺失 CAP_SYS_TIME 或 root 权限，修改时钟与 RTC 回写可能被内核拒绝。',
            })}
          </AlertDescription>
        </Alert>
      )}

      {/* 原生 shadcn-admin 风格状态面板 */}
      <div className='rounded-lg border bg-muted/30 p-4 space-y-3'>
        <div className='flex items-center justify-between'>
          <div className='flex items-center gap-2'>
            <span className='inline-block h-2 w-2 rounded-full bg-emerald-500 animate-pulse' />
            <span className='text-xs font-medium text-muted-foreground'>
              {t('dashboard.liveClock', { defaultValue: '设备实时内核时钟' })}
            </span>
            <Badge variant='outline' className='font-mono text-[10px] px-1.5 py-0'>
              {status?.timezone ?? 'Asia/Shanghai'}
            </Badge>
          </div>

          <div className='flex items-center gap-2'>
            {status?.mode === 'ntp' && (
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={onSyncNow}
                disabled={isSyncing}
                className='h-7 text-xs gap-1.5'
              >
                <RefreshCw
                  className={`h-3 w-3 ${isSyncing ? 'animate-spin' : ''}`}
                />
                {t('ntp.syncNow', { defaultValue: '立即对时' })}
              </Button>
            )}
          </div>
        </div>

        <div className='flex flex-wrap items-baseline justify-between gap-3'>
          <div className='font-mono text-2xl font-bold tracking-tight'>
            {timeString}{' '}
            <span className='text-sm font-normal text-muted-foreground'>
              {dateString}
            </span>
          </div>

          <div className='flex flex-wrap items-center gap-2'>
            {renderSyncBadge(syncState, t)}
            {renderRtcBadge(rtcState, t)}
          </div>
        </div>

        {/* 详细指标 */}
        {status?.syncStatus?.lastSyncTime && (
          <div className='flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] font-mono text-muted-foreground border-t border-border/40 pt-2'>
            <span>
              源: {status.syncStatus.lastSyncServer || '-'}
            </span>
            <span>
              RTT: {status.syncStatus.rttMs.toFixed(1)}ms
            </span>
            <span>
              偏差: {status.syncStatus.offsetMs > 0 ? '+' : ''}{status.syncStatus.offsetMs.toFixed(1)}ms
            </span>
          </div>
        )}
      </div>
    </div>
  )
}
