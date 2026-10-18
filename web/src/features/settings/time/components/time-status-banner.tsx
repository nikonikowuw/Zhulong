import { AlertTriangle, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { STATUS_DOT_CLASS, STATUS_LEVEL_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
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
          className={cn(
            'gap-1.5 text-xs font-normal',
            STATUS_LEVEL_CLASS.active
          )}
        >
          <span
            className={cn(
              'inline-block h-1.5 w-1.5 rounded-full',
              STATUS_DOT_CLASS.active
            )}
          />
          {t('dashboard.stateSynchronized', { defaultValue: '已同步' })}
        </Badge>
      )
    case 'syncing':
      return (
        <Badge
          variant='outline'
          className={cn(
            'gap-1.5 text-xs font-normal',
            STATUS_LEVEL_CLASS.warning
          )}
        >
          <span
            className={cn(
              'inline-block h-1.5 w-1.5 animate-pulse rounded-full',
              STATUS_DOT_CLASS.warning
            )}
          />
          {t('dashboard.stateSyncing', { defaultValue: '同步中' })}
        </Badge>
      )
    case 'panic_review':
      return (
        <Badge
          variant='outline'
          className={cn(
            'gap-1.5 text-xs font-normal',
            STATUS_LEVEL_CLASS.warning
          )}
        >
          <span
            className={cn(
              'inline-block h-1.5 w-1.5 rounded-full',
              STATUS_DOT_CLASS.warning
            )}
          />
          {t('dashboard.statePanicReview', { defaultValue: '复核中' })}
        </Badge>
      )
    case 'failed':
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
              'inline-block h-1.5 w-1.5 rounded-full',
              STATUS_DOT_CLASS.destructive
            )}
          />
          {t('dashboard.stateFailed', { defaultValue: '同步失败' })}
        </Badge>
      )
    default:
      return (
        <Badge
          variant='outline'
          className={cn(
            'gap-1.5 text-xs font-normal',
            STATUS_LEVEL_CLASS.inactive
          )}
        >
          <span
            className={cn(
              'inline-block h-1.5 w-1.5 rounded-full',
              STATUS_DOT_CLASS.inactive
            )}
          />
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
          className={cn(
            'gap-1.5 text-xs font-normal',
            STATUS_LEVEL_CLASS.active
          )}
        >
          <span
            className={cn(
              'inline-block h-1.5 w-1.5 rounded-full',
              STATUS_DOT_CLASS.active
            )}
          />
          {t('dashboard.rtcNormal', { defaultValue: 'RTC 就绪' })}
        </Badge>
      )
    case 'error':
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
              'inline-block h-1.5 w-1.5 rounded-full',
              STATUS_DOT_CLASS.destructive
            )}
          />
          {t('dashboard.rtcError', { defaultValue: 'RTC 异常' })}
        </Badge>
      )
    default:
      return (
        <Badge
          variant='outline'
          className={cn(
            'gap-1.5 text-xs font-normal',
            STATUS_LEVEL_CLASS.inactive
          )}
        >
          <span
            className={cn(
              'inline-block h-1.5 w-1.5 rounded-full',
              STATUS_DOT_CLASS.inactive
            )}
          />
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
        <Alert variant='destructive' className='px-3 py-2 text-xs'>
          <AlertTriangle className='h-4 w-4' />
          <AlertTitle className='text-xs font-semibold'>
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
      <div className='space-y-3 rounded-lg border bg-muted/30 p-4'>
        <div className='flex items-center justify-between'>
          <div className='flex items-center gap-2'>
            <span className='inline-block h-2 w-2 animate-pulse rounded-full bg-emerald-500' />
            <span className='text-xs font-medium text-muted-foreground'>
              {t('dashboard.liveClock', { defaultValue: '设备实时内核时钟' })}
            </span>
            <Badge
              variant='outline'
              className='px-1.5 py-0 font-mono text-[10px]'
            >
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
                className='h-7 gap-1.5 text-xs'
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
          <div className='flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-border/40 pt-2 font-mono text-[11px] text-muted-foreground'>
            <span>源: {status.syncStatus.lastSyncServer || '-'}</span>
            <span>RTT: {status.syncStatus.rttMs.toFixed(1)}ms</span>
            <span>
              偏差: {status.syncStatus.offsetMs > 0 ? '+' : ''}
              {status.syncStatus.offsetMs.toFixed(1)}ms
            </span>
          </div>
        )}
      </div>
    </div>
  )
}
