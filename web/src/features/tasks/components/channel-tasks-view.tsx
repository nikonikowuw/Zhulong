import { Settings2, Video } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { STATUS_DOT_CLASS, STATUS_LEVEL_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { type ChannelTaskGroup } from '../data/schema'
import { TaskStatusBadge } from './task-status-badge'

interface ChannelTasksViewProps {
  groups: ChannelTaskGroup[]
  onConfigureCamera: (cameraId: string) => void
}

export function ChannelTasksView({
  groups,
  onConfigureCamera,
}: ChannelTasksViewProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  if (groups.length === 0) {
    return (
      <div className='flex min-h-[300px] flex-col items-center justify-center rounded-xl border border-dashed border-border p-8 text-center'>
        <div className='flex h-12 w-12 items-center justify-center rounded-full bg-muted'>
          <Video className='h-6 w-6 text-muted-foreground' />
        </div>
        <h3 className='mt-4 text-base font-semibold'>
          {t('table.empty', '暂无分析任务记录')}
        </h3>
        <p className='mt-1 max-w-sm text-sm text-muted-foreground'>
          {t(
            'channelCard.noAlgorithms',
            '当前尚未向任何摄像头分配分析算法，点击上方“配置通道任务”即可开始。'
          )}
        </p>
      </div>
    )
  }

  return (
    <div className='grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3'>
      {groups.map((group) => {
        const isOnline = group.camera.health === 'online'
        const hasTasks = group.items.length > 0

        return (
          <div
            key={group.camera.id}
            className='flex flex-col justify-between rounded-xl border border-border bg-card p-4 shadow-xs transition-shadow hover:shadow-sm'
          >
            {/* 卡片头部：相机信息与在线徽标 */}
            <div>
              <div className='flex items-start justify-between gap-2'>
                <div className='flex items-center gap-2'>
                  <div className='flex h-8 w-8 items-center justify-center rounded-lg border border-border bg-muted/40'>
                    <Video className='h-4 w-4 text-primary' />
                  </div>
                  <div>
                    <h4 className='text-sm leading-tight font-semibold text-foreground'>
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
                    isOnline
                      ? STATUS_LEVEL_CLASS.active
                      : STATUS_LEVEL_CLASS.inactive
                  )}
                >
                  <span
                    className={cn(
                      'h-1.5 w-1.5 rounded-full',
                      isOnline
                        ? STATUS_DOT_CLASS.active
                        : STATUS_DOT_CLASS.inactive
                    )}
                  />
                  {isOnline
                    ? t('channelCard.cameraOnline', '在线')
                    : t('channelCard.cameraOffline', '离线')}
                </Badge>
              </div>

              {/* 算力消耗概要 */}
              <div className='mt-3 flex items-center justify-between border-y border-border/50 py-2 text-xs'>
                <span className='text-muted-foreground'>
                  {t('channelCard.channelPoints', '通道算力消耗')}:
                </span>
                <span className='font-semibold text-foreground'>
                  {group.totalChannelPoints}{' '}
                  {t('channelCard.pointsSuffix', '点')} ({group.activeCount} /{' '}
                  {group.totalCount})
                </span>
              </div>

              {/* 算法列表 */}
              <div className='mt-3 space-y-2'>
                {!hasTasks ? (
                  <div className='rounded-lg border border-dashed border-border/80 p-3 text-center text-xs text-muted-foreground'>
                    {t('channelCard.noAlgorithms', '暂未配置分析算法')}
                  </div>
                ) : (
                  group.items.map((item) => (
                    <div
                      key={item.algorithmItem.id}
                      className='flex items-center justify-between rounded-lg border border-border/60 bg-muted/20 px-2.5 py-1.5 text-xs'
                    >
                      <div className='flex items-center gap-2 overflow-hidden'>
                        <TaskStatusBadge
                          enabled={
                            item.task.enabled && item.algorithmItem.enabled
                          }
                          algorithmStatus={item.algorithm?.status}
                        />
                        <span className='truncate font-medium text-foreground'>
                          {item.algorithm?.name ||
                            item.algorithmItem.algorithmId}
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
                  ))
                )}
              </div>
            </div>

            {/* 卡片底部操作按钮 */}
            <div className='mt-4 pt-2'>
              <Button
                variant='outline'
                size='sm'
                className='w-full gap-2 text-xs'
                onClick={() => onConfigureCamera(group.camera.id)}
              >
                <Settings2 className='h-3.5 w-3.5' />
                {t('channelCard.configButton', '配置算法')}
              </Button>
            </div>
          </div>
        )
      })}
    </div>
  )
}
