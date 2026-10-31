import { type Column, type Row } from '@tanstack/react-table'
import {
  ChevronRight,
  Layers,
  MoreHorizontal,
  Settings2,
  Trash2,
  Video,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  STATUS_DOT_CLASS,
  STATUS_LEVEL_CLASS,
  type StatusLevel,
} from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Switch } from '@/components/ui/switch'
import { DataTableColumnHeader } from '@/components/data-table'
import { type ChannelTaskGroup, type Task } from '../data/schema'
import { useUpdateTask } from '../hooks/use-tasks'
import { TaskCopyIdButton } from './task-copy-id-button'

export function TaskColumnHeader<TData>({
  column,
  titleKey,
  titleFallback,
}: {
  column: Column<TData, unknown>
  titleKey: string
  titleFallback?: string
}) {
  const { t } = useTranslation('tasks')
  return (
    <DataTableColumnHeader
      column={column}
      title={t(titleKey, titleFallback || titleKey)}
    />
  )
}

/** 操作列表头：仅提供无障碍标签，视觉上不可见。 */
export function TaskActionsHeader() {
  const { t } = useTranslation('tasks')
  return <span className='sr-only'>{t('table.actions', '操作')}</span>
}

/** 折叠/展开控制单元格 */
export function ChannelExpanderCell({ row }: { row: Row<ChannelTaskGroup> }) {
  const { t } = useTranslation('tasks')
  const isExpanded = row.getIsExpanded()
  const label = isExpanded
    ? t('table.collapseRow', '折叠明细')
    : t('table.expandRow', '展开明细')

  return (
    <Button
      variant='ghost'
      size='icon'
      className='size-7 p-0 text-muted-foreground transition-transform hover:text-foreground'
      onClick={row.getToggleExpandedHandler()}
      aria-label={label}
      title={label}
    >
      <ChevronRight
        className={cn(
          'size-4 transition-transform duration-200',
          isExpanded && 'rotate-90'
        )}
      />
    </Button>
  )
}

/** 关联摄像头单元格 */
export function ChannelCameraCell({ row }: { row: Row<ChannelTaskGroup> }) {
  const { t } = useTranslation('tasks')
  const group = row.original
  const isOnline = group.camera.health === 'online'
  const isReconnecting = group.camera.session === 'reconnecting'
  const isError =
    group.camera.health === 'error' || group.camera.session === 'error'
  const healthLevel: StatusLevel = isOnline
    ? 'active'
    : isReconnecting
      ? 'warning'
      : isError
        ? 'destructive'
        : 'inactive'
  const taskId = group.task?.id

  return (
    <div className='flex min-w-0 items-center gap-2.5'>
      <div className='flex size-8 shrink-0 items-center justify-center rounded-lg border border-border bg-muted/60 text-muted-foreground'>
        <Video className='size-4' />
      </div>
      <div className='flex min-w-0 flex-col gap-0.5'>
        <div className='flex items-center gap-2'>
          <span className='truncate text-xs font-semibold text-foreground'>
            {group.camera.name}
          </span>
          <Badge
            variant='outline'
            className={cn(
              'shrink-0 gap-1 px-1.5 py-0 text-[10px] font-normal',
              STATUS_LEVEL_CLASS[healthLevel]
            )}
          >
            <span
              className={cn(
                'size-1.5 rounded-full',
                STATUS_DOT_CLASS[healthLevel]
              )}
            />
            {isOnline
              ? t('channelCard.cameraOnline', '在线')
              : isReconnecting
                ? t('channelCard.cameraReconnecting', '重连中')
                : t('channelCard.cameraOffline', '离线')}
          </Badge>
        </div>
        <div className='flex items-center gap-2 text-[11px] text-muted-foreground'>
          <span>
            {group.camera.streams?.[0]?.protocol?.toUpperCase() || 'RTSP'} ·{' '}
            {group.camera.streams?.[0]?.role === 'main'
              ? t('channelCard.mainStream', '主码流')
              : t('channelCard.subStream', '子码流')}
          </span>
          {taskId && (
            <span className='inline-flex items-center gap-1 font-mono text-[11px] text-muted-foreground/80'>
              <span className='text-border'>|</span>
              <span title={taskId}>
                {t('table.id', '任务 ID')}: {taskId.slice(0, 8)}...
              </span>
              <TaskCopyIdButton id={taskId} label='task' />
            </span>
          )}
        </div>
      </div>
    </div>
  )
}

/** 通道分析总开关单元格 */
export function ChannelStatusCell({ row }: { row: Row<ChannelTaskGroup> }) {
  const { t } = useTranslation('tasks')
  const updateMutation = useUpdateTask()
  const task = row.original.task
  const camera = row.original.camera

  if (!task) return null

  const handleToggle = (checked: boolean) => {
    updateMutation.mutate(
      {
        id: task.id,
        data: { enabled: checked },
      },
      {
        onSuccess: () => {
          toast.success(
            checked
              ? t('table.channelResumed', '通道分析任务已恢复运行')
              : t('table.channelPaused', '通道分析任务已暂停')
          )
        },
        onError: () => {
          toast.error(t('table.toggleError', '更新任务状态失败'))
        },
      }
    )
  }

  const isCameraDisabled = !camera.enabled
  const isStreamOffline = camera.health === 'offline'
  const isStreamReconnecting = camera.session === 'reconnecting'
  const isStreamError = camera.health === 'error' || camera.session === 'error'

  let statusText = task.enabled
    ? t('channelCard.channelRunning', '通道已启用')
    : t('channelCard.channelPaused', '通道已暂停')

  if (task.enabled) {
    if (isCameraDisabled) {
      statusText = t('table.statusCameraDisabled', '摄像机已停用')
    } else if (isStreamReconnecting) {
      statusText = t('table.statusStreamReconnecting', '视频流重连中')
    } else if (isStreamError) {
      statusText = t('table.statusStreamError', '视频流异常')
    } else if (isStreamOffline || camera.health !== 'online') {
      statusText = t('table.statusStreamDisconnected', '视频流断开')
    }
  }

  return (
    <div className='flex items-center gap-2'>
      <Switch
        checked={task.enabled}
        disabled={updateMutation.isPending}
        onCheckedChange={handleToggle}
        aria-label={t('channelCard.toggleChannel', '切换通道分析状态')}
      />
      <span
        className={cn(
          'text-xs text-muted-foreground',
          task.enabled &&
            (isStreamOffline || camera.health !== 'online') &&
            'font-medium text-destructive',
          task.enabled && isStreamReconnecting && 'font-medium text-amber-500'
        )}
      >
        {statusText}
      </span>
    </div>
  )
}

/** 算法订阅概览单元格 */
export function ChannelAlgorithmsCell({ row }: { row: Row<ChannelTaskGroup> }) {
  const { t } = useTranslation('tasks')
  const group = row.original

  return (
    <div className='flex items-center gap-2'>
      <Badge variant='outline' className='gap-1 font-mono text-xs font-normal'>
        <Layers className='size-3 text-muted-foreground' />
        {t('channelCard.itemsCount', '{{count}} 项', {
          count: group.items.length,
        })}
      </Badge>
      <span className='text-[11px] text-muted-foreground'>
        ({group.activeCount} {t('budgetCard.activeTasks', '活跃')})
      </span>
    </div>
  )
}

/** 通道总算力单元格 */
export function ChannelPointsCell({ row }: { row: Row<ChannelTaskGroup> }) {
  const { t } = useTranslation('tasks')
  const group = row.original

  return (
    <div className='flex items-center gap-1 text-xs'>
      <span className='font-mono font-semibold text-foreground'>
        {group.totalChannelPoints}
      </span>
      <span className='text-[11px] text-muted-foreground'>
        {t('channelCard.pointsTotalSuffix', '点总开销')}
      </span>
    </div>
  )
}

/** 更新时间单元格 */
export function ChannelUpdatedAtCell({ row }: { row: Row<ChannelTaskGroup> }) {
  const raw = row.original.task?.updatedAt
  if (!raw) return <span className='text-xs text-muted-foreground'>-</span>

  return (
    <span className='font-mono text-xs whitespace-nowrap text-muted-foreground'>
      {new Date(raw).toLocaleString()}
    </span>
  )
}

/** 通道级操作单元格 */
export function ChannelActionsCell({
  row,
  onConfigureCamera,
  onDeleteChannelTask,
}: {
  row: Row<ChannelTaskGroup>
  onConfigureCamera?: (cameraId: string) => void
  onDeleteChannelTask?: (task: Task) => void
}) {
  const { t } = useTranslation('tasks')
  const group = row.original
  const task = group.task

  return (
    <div className='flex items-center justify-end gap-1.5'>
      <Button
        variant='outline'
        size='sm'
        className='h-7 gap-1 px-2.5 text-xs font-medium'
        onClick={() => onConfigureCamera?.(group.camera.id)}
      >
        <Settings2 className='size-3' />
        <span>{t('channelCard.configButton', '配置算法')}</span>
      </Button>
      {task && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant='ghost'
              size='icon'
              className='size-7 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground'
              aria-label={t('channelCard.moreActions', '更多操作')}
            >
              <MoreHorizontal className='size-3.5' />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align='end' className='w-36'>
            <DropdownMenuItem
              onClick={() => onDeleteChannelTask?.(task)}
              className='text-destructive focus:text-destructive'
            >
              <Trash2 className='me-2 size-3.5' />
              {t('channelCard.deleteButton', '删除通道任务')}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  )
}
