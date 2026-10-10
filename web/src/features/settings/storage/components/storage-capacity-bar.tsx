import { useMemo } from 'react'
import {
  HardDrive,
  AlertTriangle,
  CheckCircle2,
  AlertCircle,
  RefreshCw,
  FolderLock,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { formatBytes } from '../utils/format'
import { type StorageHealthStatus, type StorageStatus } from '../types'

interface StorageCapacityBarProps {
  status: StorageStatus | null
  isLoading?: boolean
}

export function StorageCapacityBar({
  status,
  isLoading,
}: StorageCapacityBarProps) {
  const { t } = useTranslation('storage')

  const total = status?.totalBytes ?? 0
  const free = status?.freeBytes ?? 0
  const used = status?.usedBytes ?? 0
  const usagePercent = status?.usagePercent ?? 0

  const recordings = status?.breakdown.recordingsBytes ?? 0
  const snapshots = status?.breakdown.snapshotsBytes ?? 0
  const exportsBytes = status?.breakdown.exportsBytes ?? 0
  const other = status?.breakdown.otherBytes ?? 0

  const percentages = useMemo(() => {
    if (total <= 0) return { recordings: 0, snapshots: 0, exports: 0, other: 0 }
    return {
      recordings: Math.max(0, (recordings / total) * 100),
      snapshots: Math.max(0, (snapshots / total) * 100),
      exports: Math.max(0, (exportsBytes / total) * 100),
      other: Math.max(0, (other / total) * 100),
    }
  }, [total, recordings, snapshots, exportsBytes, other])

  const renderStatusBadge = (healthStatus?: StorageHealthStatus) => {
    switch (healthStatus) {
      case 'healthy':
        return (
          <Badge
            variant='outline'
            className='border-emerald-500/30 bg-emerald-500/10 text-emerald-500 gap-1'
          >
            <CheckCircle2 className='size-3.5' />
            {t('status.healthy', { defaultValue: '运行正常' })}
          </Badge>
        )
      case 'warning':
        return (
          <Badge
            variant='outline'
            className='border-amber-500/30 bg-amber-500/10 text-amber-500 gap-1'
          >
            <AlertTriangle className='size-3.5' />
            {t('status.warning', { defaultValue: '高水位预警' })}
          </Badge>
        )
      case 'cleaning':
        return (
          <Badge
            variant='outline'
            className='border-blue-500/30 bg-blue-500/10 text-blue-500 gap-1'
          >
            <RefreshCw className='size-3.5 animate-spin' />
            {t('status.cleaning', { defaultValue: '清理让渡中' })}
          </Badge>
        )
      case 'emergency_stopped':
        return (
          <Badge variant='destructive' className='gap-1'>
            <AlertCircle className='size-3.5' />
            {t('status.emergency_stopped', { defaultValue: '停录熔断' })}
          </Badge>
        )
      case 'error':
        return (
          <Badge variant='destructive' className='gap-1'>
            <AlertCircle className='size-3.5' />
            {t('status.error', { defaultValue: '设备异常/掉盘' })}
          </Badge>
        )
      default:
        return (
          <Badge variant='secondary'>
            {t('status.unknown', { defaultValue: '未知状态' })}
          </Badge>
        )
    }
  }

  return (
    <Card className='border-border/60 bg-card/60 backdrop-blur-sm'>
      <CardHeader className='pb-3'>
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <div className='flex items-center gap-2'>
            <HardDrive className='size-5 text-muted-foreground' />
            <CardTitle className='text-base font-semibold'>
              {t('overview.title', { defaultValue: '存储容量与健康大盘' })}
            </CardTitle>
            {renderStatusBadge(status?.status)}
          </div>
          <div className='flex flex-wrap items-center gap-2 text-xs'>
            {status?.isExternal ? (
              <Badge variant='outline' className='bg-primary/5 text-primary'>
                {t('overview.dedicated_mount', {
                  defaultValue: '独立外挂盘',
                })}
              </Badge>
            ) : (
              <Badge
                variant='outline'
                className='text-amber-500 border-amber-500/30'
              >
                {t('overview.root_fs', { defaultValue: '系统根分区' })}
              </Badge>
            )}
            <Badge
              variant={status?.canWrite ? 'outline' : 'destructive'}
              className='gap-1'
            >
              {status?.canWrite ? (
                <span>{t('overview.write_ok', { defaultValue: '允许写盘' })}</span>
              ) : (
                <>
                  <FolderLock className='size-3' />
                  <span>{t('overview.write_blocked', { defaultValue: '禁止写入' })}</span>
                </>
              )}
            </Badge>
          </div>
        </div>
      </CardHeader>

      <CardContent className='space-y-4'>
        {/* Metric Overview Figures */}
        <div className='grid grid-cols-2 gap-4 sm:grid-cols-4'>
          <div className='space-y-0.5'>
            <span className='text-xs text-muted-foreground'>
              {t('overview.total', { defaultValue: '总容量' })}
            </span>
            <p className='text-lg font-bold tracking-tight'>
              {formatBytes(total)}
            </p>
          </div>
          <div className='space-y-0.5'>
            <span className='text-xs text-muted-foreground'>
              {t('overview.used', { defaultValue: '已用空间' })}
            </span>
            <p className='text-lg font-bold tracking-tight text-foreground'>
              {formatBytes(used)}{' '}
              <span className='text-xs font-normal text-muted-foreground'>
                ({usagePercent}%)
              </span>
            </p>
          </div>
          <div className='space-y-0.5'>
            <span className='text-xs text-muted-foreground'>
              {t('overview.free', { defaultValue: '可用剩余' })}
            </span>
            <p className='text-lg font-bold tracking-tight text-emerald-500'>
              {formatBytes(free)}
            </p>
          </div>
          <div className='space-y-0.5'>
            <span className='text-xs text-muted-foreground'>
              {t('overview.mountPoint', { defaultValue: '挂载点与文件系统' })}
            </span>
            <p className='truncate text-sm font-medium' title={status?.mountPoint}>
              {status?.mountPoint || '-'} ({status?.fsType || 'unknown'})
            </p>
          </div>
        </div>

        {/* Multi-segment Progress Bar */}
        <div className='space-y-1.5'>
          <div
            role='progressbar'
            aria-valuenow={usagePercent}
            aria-valuemin={0}
            aria-valuemax={100}
            className='relative flex h-3.5 w-full overflow-hidden rounded-full bg-secondary'
          >
            {/* Recordings */}
            <div
              style={{ width: `${percentages.recordings}%` }}
              className='bg-blue-500 transition-all duration-300'
              title={`录像: ${formatBytes(recordings)} (${percentages.recordings.toFixed(1)}%)`}
            />
            {/* Snapshots */}
            <div
              style={{ width: `${percentages.snapshots}%` }}
              className='bg-purple-500 transition-all duration-300'
              title={`抓拍: ${formatBytes(snapshots)} (${percentages.snapshots.toFixed(1)}%)`}
            />
            {/* Exports */}
            <div
              style={{ width: `${percentages.exports}%` }}
              className='bg-amber-500 transition-all duration-300'
              title={`导出: ${formatBytes(exportsBytes)} (${percentages.exports.toFixed(1)}%)`}
            />
            {/* Other System Files */}
            <div
              style={{ width: `${percentages.other}%` }}
              className='bg-slate-400 dark:bg-slate-600 transition-all duration-300'
              title={`系统其它: ${formatBytes(other)} (${percentages.other.toFixed(1)}%)`}
            />
          </div>

          {/* Color Legend */}
          <div className='flex flex-wrap items-center justify-between gap-y-1 text-xs text-muted-foreground'>
            <div className='flex flex-wrap items-center gap-4'>
              <div className='flex items-center gap-1.5'>
                <span className='size-2.5 rounded-full bg-blue-500' />
                <span>
                  {t('breakdown.recordings', { defaultValue: '视频录像' })}:{' '}
                  <strong className='text-foreground'>{formatBytes(recordings)}</strong>
                </span>
              </div>
              <div className='flex items-center gap-1.5'>
                <span className='size-2.5 rounded-full bg-purple-500' />
                <span>
                  {t('breakdown.snapshots', { defaultValue: 'AI抓拍' })}:{' '}
                  <strong className='text-foreground'>{formatBytes(snapshots)}</strong>
                </span>
              </div>
              <div className='flex items-center gap-1.5'>
                <span className='size-2.5 rounded-full bg-amber-500' />
                <span>
                  {t('breakdown.exports', { defaultValue: '临时导出' })}:{' '}
                  <strong className='text-foreground'>{formatBytes(exportsBytes)}</strong>
                </span>
              </div>
              <div className='flex items-center gap-1.5'>
                <span className='size-2.5 rounded-full bg-slate-400 dark:bg-slate-600' />
                <span>
                  {t('breakdown.other', { defaultValue: '其它占用' })}:{' '}
                  <strong className='text-foreground'>{formatBytes(other)}</strong>
                </span>
              </div>
            </div>
            {isLoading && (
              <span className='flex items-center gap-1 text-xs text-muted-foreground'>
                <RefreshCw className='size-3 animate-spin' />
                {t('overview.refreshing', { defaultValue: '遥测刷新中...' })}
              </span>
            )}
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
