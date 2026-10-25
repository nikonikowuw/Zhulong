import { Plus, Settings2, Trash2, Video } from 'lucide-react'
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
        const healthLevel: StatusLevel = isOnline ? 'active' : 'inactive'
        // 已过滤掉空通道，因此每个卡片必然至少有一个订阅项
        const primaryTask = group.items[0].task

        return (
          <Card
            key={group.camera.id}
            className='gap-3 py-4 transition-shadow hover:shadow-md'
          >
            {/* 卡片头部：相机信息与在线徽标 */}
            <CardHeader className='px-4'>
              <div className='flex items-start justify-between gap-2'>
                <div className='flex items-center gap-2.5'>
                  <div className='flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary'>
                    <Video className='size-4' />
                  </div>
                  <div className='min-w-0'>
                    <h4 className='truncate text-sm leading-tight font-semibold text-foreground'>
                      {group.camera.name}
                    </h4>
                    <span className='text-xs text-muted-foreground'>
                      {group.camera.streams?.[0]?.protocol?.toUpperCase() ||
                        'RTSP'}{' '}
                      ·{' '}
                      {group.camera.streams?.[0]?.role === 'main'
                        ? t('channelCard.mainStream', '主码流')
                        : t('channelCard.subStream', '子码流')}
                    </span>
                  </div>
                </div>

                <Badge
                  variant='outline'
                  className={cn(
                    'gap-1.5 text-xs font-normal',
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
                    : t('channelCard.cameraOffline', '离线')}
                </Badge>
              </div>
            </CardHeader>

            <CardContent className='space-y-3 px-4'>
              {/* 算力消耗概要与通道主开关 */}
              <div className='flex items-center justify-between border-y border-border/60 py-2 text-xs'>
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
                  <span className='text-muted-foreground'>
                    {primaryTask.enabled
                      ? t('channelCard.channelRunning', '通道已启用')
                      : t('channelCard.channelPaused', '通道已暂停')}
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

              {/* 算法列表 */}
              <div className='space-y-2'>
                {group.items.map((item) => (
                  <div
                    key={item.algorithmItem.id}
                    className='flex items-center justify-between rounded-lg border border-border/60 bg-muted/30 px-2.5 py-1.5 text-xs'
                  >
                    <div className='flex items-center gap-2 overflow-hidden'>
                      <TaskStatusBadge
                        enabled={
                          item.task.enabled && item.algorithmItem.enabled
                        }
                        isChannelEnabled={item.task.enabled}
                        algorithmStatus={item.algorithm?.status}
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
            <CardFooter className='px-4'>
              <div className='flex w-full items-center gap-2'>
                <Button
                  variant='outline'
                  size='sm'
                  className='h-8 flex-1 gap-1.5 text-xs'
                  onClick={() => onConfigureCamera(group.camera.id)}
                >
                  <Settings2 className='size-3.5' />
                  {t('channelCard.configButton', '配置算法')}
                </Button>
                <Button
                  variant='ghost'
                  size='icon'
                  className='size-8 text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive'
                  title={t('channelCard.deleteButton', '删除通道任务')}
                  aria-label={t('channelCard.deleteButton', '删除通道任务')}
                  onClick={() => onDeleteChannelTask?.(primaryTask)}
                >
                  <Trash2 className='size-3.5' />
                </Button>
              </div>
            </CardFooter>
          </Card>
        )
      })}
    </div>
  )
}
