import { MoreHorizontal, Plus, Settings2, Trash2, Video } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  STATUS_DOT_CLASS,
  STATUS_LEVEL_CLASS,
  type StatusLevel,
} from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardFooter, CardHeader } from '@/components/ui/card'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Switch } from '@/components/ui/switch'
import { type ChannelTaskGroup, type Task } from '../data/schema'
import { TaskStatusBadge } from './task-status-badge'

interface ChannelTasksViewProps {
  groups: ChannelTaskGroup[]
  /** 传入 cameraId 时为该通道配置；不传则进入「选择摄像头」入口 */
  onConfigureCamera: (cameraId?: string) => void
  onToggleChannelTask?: (task: Task) => void
  onDeleteChannelTask?: (task: Task) => void
}

export function ChannelTasksView({
  groups,
  onConfigureCamera,
  onToggleChannelTask,
  onDeleteChannelTask,
}: ChannelTasksViewProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  // 通道卡片视图只展示**已配置算法**的通道：没有任务的摄像头不该占一格卡片，
  // 否则任务清空后页面上会残留一张空卡片（职责上未配置的通道由主 CTA 接管）。
  const configuredGroups = groups.filter((group) => group.items.length > 0)

  if (configuredGroups.length === 0) {
    return (
      <div className='flex min-h-[300px] flex-col items-center justify-center rounded-xl border border-dashed border-border p-8 text-center'>
        <div className='flex size-12 items-center justify-center rounded-full bg-muted'>
          <Video className='size-6 text-muted-foreground' />
        </div>
        <h3 className='mt-4 text-base font-semibold'>
          {t('table.empty', '暂无分析任务记录')}
        </h3>
        <p className='mt-1 max-w-sm text-sm text-muted-foreground'>
          {t(
            'table.emptyDesc',
            '尚未向任何摄像头分配分析算法，点击「配置通道任务」开始。'
          )}
        </p>
        <Button size='sm' className='mt-4' onClick={() => onConfigureCamera()}>
          <Plus className='me-1 size-4' />
          {t('newChannelTask', '配置通道任务')}
        </Button>
      </div>
    )
  }

  return (
    <div className='grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3'>
      {configuredGroups.map((group) => {
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
        // 已过滤掉空通道，因此每个卡片必然至少有一个订阅项
        const primaryTask = group.items[0].task

        return (
          <Card
            key={group.camera.id}
            className='flex flex-col justify-between gap-0 overflow-hidden py-0 shadow-xs transition-all hover:border-primary/40 hover:shadow-md'
          >
            {/* 卡片头部：相机信息与在线徽标 */}
            <CardHeader className='border-b border-border/40 bg-muted/20 px-4 py-3'>
              <div className='flex items-start justify-between gap-2'>
                <div className='flex min-w-0 items-center gap-2.5'>
                  <div className='flex size-8 shrink-0 items-center justify-center rounded-lg border border-border bg-muted/60 text-muted-foreground'>
                    <Video className='size-4' />
                  </div>
                  <div className='min-w-0'>
                    <h4 className='truncate text-sm leading-tight font-semibold text-foreground'>
                      {group.camera.name}
                    </h4>
                    <span className='text-[11px] text-muted-foreground'>
                      {group.camera.streams?.[0]?.protocol?.toUpperCase() ||
                        'RTSP'}{' '}
                      ·{' '}
                      {group.camera.streams?.[0]?.role === 'main'
                        ? t('channelCard.mainStream', '主码流')
                        : t('channelCard.subStream', '子码流')}
                    </span>
                  </div>
                </div>

                <div className='flex items-center gap-1.5'>
                  <Badge
                    variant='outline'
                    className={cn(
                      'shrink-0 gap-1.5 px-2 py-0.5 text-[11px] font-normal',
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

                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button
                        variant='ghost'
                        size='icon'
                        className='size-7 text-muted-foreground hover:bg-muted hover:text-foreground'
                        aria-label={t('channelCard.moreActions', '更多操作')}
                      >
                        <MoreHorizontal className='size-3.5' />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align='end' className='w-40'>
                      <DropdownMenuItem
                        onClick={() => onConfigureCamera(group.camera.id)}
                      >
                        <Settings2 className='me-2 size-3.5' />
                        {t('channelCard.configButton', '配置算法')}
                      </DropdownMenuItem>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem
                        onClick={() => onDeleteChannelTask?.(primaryTask)}
                        className='text-destructive focus:text-destructive'
                      >
                        <Trash2 className='me-2 size-3.5' />
                        {t('channelCard.deleteButton', '删除通道任务')}
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>
              </div>
            </CardHeader>

            <CardContent className='flex flex-1 flex-col gap-3 p-4'>
              {/* 算力消耗概要与通道主开关 */}
              <div className='flex items-center justify-between rounded-lg bg-muted/40 px-3 py-2 text-xs'>
                <div>
                  <span className='text-muted-foreground'>
                    {t('channelCard.channelPoints', '通道算力消耗')}:{' '}
                  </span>
                  <span className='font-semibold text-foreground'>
                    {group.totalChannelPoints}{' '}
                    {t('channelCard.pointsSuffix', '点')} ({group.activeCount} /{' '}
                    {group.totalCount})
                  </span>
                </div>

                <div className='flex items-center gap-1.5'>
                  <span
                    className={cn(
                      'text-xs text-muted-foreground',
                      primaryTask.enabled &&
                        (group.camera.health === 'offline' ||
                          group.camera.health !== 'online') &&
                        'font-medium text-destructive',
                      primaryTask.enabled &&
                        group.camera.session === 'reconnecting' &&
                        'font-medium text-amber-500'
                    )}
                  >
                    {!primaryTask.enabled
                      ? t('channelCard.channelPaused', '通道已暂停')
                      : !group.camera.enabled
                        ? t('table.statusCameraDisabled', '摄像机已停用')
                        : group.camera.session === 'reconnecting'
                          ? t('table.statusStreamReconnecting', '视频流重连中')
                          : group.camera.health === 'error' ||
                              group.camera.session === 'error'
                            ? t('table.statusStreamError', '视频流异常')
                            : group.camera.health === 'offline' ||
                                group.camera.health !== 'online'
                              ? t(
                                  'table.statusStreamDisconnected',
                                  '视频流断开'
                                )
                              : t('channelCard.channelRunning', '通道已启用')}
                  </span>
                  <Switch
                    checked={primaryTask.enabled}
                    onCheckedChange={() => onToggleChannelTask?.(primaryTask)}
                    aria-label={t(
                      'channelCard.toggleChannel',
                      '切换通道分析状态'
                    )}
                  />
                </div>
              </div>

              {/* 算法列表（设置统一定高与自适应滚动，杜绝瀑布流参差） */}
              <div className='flex max-h-[148px] min-h-[96px] flex-col gap-1.5 overflow-y-auto pe-1'>
                {group.items.map((item) => (
                  <div
                    key={item.algorithmItem.id}
                    className='flex items-center justify-between rounded-md border border-border/40 bg-card/60 px-2.5 py-1.5 text-xs transition-colors hover:bg-muted/40'
                  >
                    <div className='flex items-center gap-2 overflow-hidden'>
                      <TaskStatusBadge
                        enabled={
                          item.task.enabled && item.algorithmItem.enabled
                        }
                        isChannelEnabled={item.task.enabled}
                        algorithmStatus={item.algorithm?.status}
                        cameraHealth={group.camera.health}
                        cameraSession={group.camera.session}
                        isCameraEnabled={group.camera.enabled}
                      />
                      <span className='truncate font-medium text-foreground'>
                        {item.algorithm?.name || item.algorithmItem.algorithmId}
                      </span>
                    </div>

                    <div className='flex shrink-0 items-center gap-1.5 text-muted-foreground'>
                      <span>{item.algorithmItem.sampleFps} FPS</span>
                      <span>·</span>
                      <span className='font-mono font-medium text-foreground'>
                        {item.computePoints}{' '}
                        {t('channelCard.pointsSuffix', '点')}
                      </span>
                    </div>
                  </div>
                ))}
              </div>
            </CardContent>

            {/* 卡片底部操作按钮 */}
            <CardFooter className='border-t border-border/40 bg-muted/10 px-4 py-2.5'>
              <Button
                variant='outline'
                size='sm'
                className='h-8 w-full gap-1.5 text-xs font-medium'
                onClick={() => onConfigureCamera(group.camera.id)}
              >
                <Settings2 className='size-3.5' />
                {t('channelCard.configButton', '配置算法')}
              </Button>
            </CardFooter>
          </Card>
        )
      })}
    </div>
  )
}
