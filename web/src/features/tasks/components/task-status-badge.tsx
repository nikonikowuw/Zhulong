import { useTranslation } from 'react-i18next'
import {
  STATUS_DOT_CLASS,
  STATUS_LEVEL_CLASS,
  type StatusLevel,
} from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { type AlgorithmStatus } from '@/features/algorithms/data/schema'
import {
  type CameraHealth,
  type CameraSession,
} from '@/features/cameras/contract'
import {
  isScheduleActiveAt,
  isScheduleAlwaysActive,
  parseSchedule,
} from '../data/schedule-config'

export interface TaskStatusBadgeProps {
  enabled: boolean
  isChannelEnabled?: boolean
  algorithmStatus?: AlgorithmStatus
  cameraHealth?: CameraHealth
  cameraSession?: CameraSession
  isCameraEnabled?: boolean
  scheduleJson?: string
  className?: string
}

export function TaskStatusBadge({
  enabled,
  isChannelEnabled,
  algorithmStatus,
  cameraHealth,
  cameraSession,
  isCameraEnabled,
  scheduleJson,
  className = '',
}: TaskStatusBadgeProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  let level: StatusLevel = 'inactive'
  let label =
    isCameraEnabled === false
      ? t('table.statusCameraDisabled', '摄像机已停用')
      : isChannelEnabled === false
        ? t('table.statusChannelPaused', '通道已暂停')
        : t('table.statusPaused', '已暂停')

  if (enabled && isChannelEnabled !== false && isCameraEnabled !== false) {
    const sched = parseSchedule(scheduleJson)
    const hasCustomSchedule = !isScheduleAlwaysActive(sched)
    const inSchedule = isScheduleActiveAt(sched)

    if (hasCustomSchedule && !inSchedule) {
      level = 'inactive'
      label = t('table.statusScheduleDormant', '休眠中 (非计划时段)')
    } else if (algorithmStatus === 'DISABLED') {
      level = 'warning'
      label = t('table.statusStandby', '待命 (未激活)')
    } else if (
      algorithmStatus === 'CRASH_LOOP' ||
      algorithmStatus === 'CRASHED'
    ) {
      level = 'destructive'
      label = t('table.statusError', '异常')
    } else if (cameraSession === 'reconnecting') {
      level = 'warning'
      label = t('table.statusStreamReconnecting', '视频流重连中')
    } else if (cameraHealth === 'error' || cameraSession === 'error') {
      level = 'destructive'
      label = t('table.statusStreamError', '视频流异常')
    } else if (cameraHealth === 'offline' || cameraHealth === 'unknown') {
      level = 'destructive'
      label = t('table.statusStreamDisconnected', '视频流断开')
    } else if (cameraHealth !== undefined && cameraHealth !== 'online') {
      level = 'destructive'
      label = t('table.statusStreamDisconnected', '视频流断开')
    } else if (hasCustomSchedule) {
      level = 'active'
      label = t('table.statusRunningScheduled', '运行中 (计划内)')
    } else {
      level = 'active'
      label = t('table.statusRunning', '运行中')
    }
  }

  const badgeClass = STATUS_LEVEL_CLASS[level]
  const dotClass = STATUS_DOT_CLASS[level]

  return (
    <Badge
      variant='outline'
      className={cn(
        'inline-flex items-center gap-1.5 text-xs font-normal',
        badgeClass,
        className
      )}
    >
      <span className={cn('inline-block h-1.5 w-1.5 rounded-full', dotClass)} />
      {label}
    </Badge>
  )
}
