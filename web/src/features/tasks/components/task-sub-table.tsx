import { Copy, Cpu, Layers, MoreHorizontal, Pencil, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { categoryBadgeClass } from '@/lib/category-palette'
import { copyToClipboard } from '@/lib/clipboard'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { REGION_CATEGORY_HUE, parseRegionConfig } from '../data/region-config'
import {
  type ChannelTaskGroup,
  type ChannelTaskItem,
  type Task,
} from '../data/schema'
import { taskAlgorithmsPayload } from '../data/task-algorithm-payload'
import { DEFAULT_COMPUTE_COST } from '../hooks/use-task-compute-budget'
import { useUpdateTask } from '../hooks/use-tasks'
import { TaskCopyIdButton } from './task-copy-id-button'
import { TaskStatusBadge } from './task-status-badge'
import { useTasksActions } from './tasks-provider'

interface TaskSubTableProps {
  group: ChannelTaskGroup
}

function SubRowActions({ item, task }: { item: ChannelTaskItem; task: Task }) {
  const { t } = useTranslation('tasks')
  const { setOpen, setCurrentRow, setTargetCameraId, setTargetAlgorithmId } =
    useTasksActions()
  const updateMutation = useUpdateTask()
  const subId = item.algorithmItem.id
  const isLastAlgorithm = (task.algorithms?.length ?? 0) <= 1

  const handleCopy = async (value: string, message: string) => {
    if (!(await copyToClipboard(value))) {
      toast.error(t('table.copyIdFailed', '复制失败，请手动复制'))
      return
    }
    toast.success(message)
  }

  const handleRemove = () => {
    if (isLastAlgorithm) {
      setCurrentRow(task)
      setOpen('delete')
      return
    }

    updateMutation.mutate(
      {
        id: task.id,
        data: {
          algorithms: taskAlgorithmsPayload(task).filter(
            (algo) => algo.id !== subId
          ),
        },
      },
      {
        onSuccess: () => {
          toast.success(t('table.removeAlgoSuccess', '已从通道中移除该算法'))
        },
        onError: () => {
          toast.error(t('table.removeAlgoError', '移除算法失败'))
        },
      }
    )
  }

  return (
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger asChild>
        <Button
          variant='ghost'
          className='flex size-7 p-0 data-[state=open]:bg-muted'
          aria-label={t('table.actions', '操作')}
        >
          <MoreHorizontal className='size-3.5' />
          <span className='sr-only'>{t('table.actions', '操作')}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end' className='w-44'>
        <DropdownMenuItem
          onClick={() => {
            setCurrentRow(task)
            setTargetCameraId(task.cameraId)
            setTargetAlgorithmId(item.algorithmItem.algorithmId)
            setOpen('channelTask')
          }}
        >
          <Pencil className='me-2 size-3.5' />
          {t('actions.edit', '编辑配置')}
        </DropdownMenuItem>

        <DropdownMenuItem
          onClick={() =>
            void handleCopy(
              subId,
              t('table.copySubscriptionIdSuccess', '订阅 ID 已复制到剪贴板')
            )
          }
        >
          <Copy className='me-2 size-3.5' />
          {t('table.copySubscriptionId', '复制订阅 ID')}
        </DropdownMenuItem>

        <DropdownMenuSeparator />

        <DropdownMenuItem
          onClick={handleRemove}
          className='text-destructive focus:text-destructive'
        >
          <Trash2 className='me-2 size-3.5' />
          {isLastAlgorithm
            ? t('table.deleteTask', '删除任务')
            : t('table.removeAlgo', '移除该算法')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/** 通道主行的二级明细表：一个相机下的算法订阅实例。 */
export function TaskSubTable({ group }: TaskSubTableProps): React.JSX.Element {
  const { t } = useTranslation('tasks')
  const task = group.task

  if (!task || group.items.length === 0) {
    return (
      <div className='p-4 text-center text-xs text-muted-foreground'>
        {t('channelCard.noAlgorithms', '暂未配置分析算法')}
      </div>
    )
  }

  return (
    <div className='border-y border-border/50 bg-muted/20 px-6 py-3'>
      <div className='flex items-center gap-1.5 pb-2 text-[11px] font-semibold text-muted-foreground'>
        <Layers className='size-3.5 text-muted-foreground' />
        <span>{t('table.subTableTitle', '绑定的算法实例明细')}</span>
        <span className='font-mono'>({group.items.length})</span>
      </div>

      <div className='overflow-hidden rounded-lg border border-border/60 bg-card/80 shadow-xs'>
        <Table>
          <TableHeader className='bg-muted/40 text-[11px]'>
            <TableRow className='h-8 hover:bg-transparent'>
              <TableHead className='h-8 text-xs font-medium'>
                {t('table.columns.algorithm', '算法包')}
              </TableHead>
              <TableHead className='h-8 w-28 text-xs font-medium'>
                {t('table.columns.sampleFps', '采样帧率')}
              </TableHead>
              <TableHead className='h-8 w-32 text-xs font-medium'>
                {t('table.columns.computePoints', '算力开销')}
              </TableHead>
              <TableHead className='h-8 w-32 text-xs font-medium'>
                {t('table.columns.status', '运行状态')}
              </TableHead>
              <TableHead className='h-8 text-xs font-medium'>
                {t('region.entryLabel', '区域过滤策略')}
              </TableHead>
              <TableHead className='h-8 w-12 text-end'>
                <span className='sr-only'>{t('table.actions', '操作')}</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {group.items.map((item) => {
              const subscription = item.algorithmItem
              const isItemEnabled = task.enabled && subscription.enabled
              const region =
                item.region ?? parseRegionConfig(subscription.regionConfigJson)
              const hasRegion = region.roi.length > 0 || region.mask.length > 0
              const costPerFrame =
                item.algorithm?.computeCost ?? DEFAULT_COMPUTE_COST

              return (
                <TableRow
                  key={subscription.id}
                  className='h-10 transition-colors hover:bg-muted/40'
                >
                  <TableCell className='py-2 text-xs font-medium'>
                    <div className='flex items-center gap-2.5'>
                      <div className='flex size-6 shrink-0 items-center justify-center rounded-md border border-border bg-muted/60 text-muted-foreground'>
                        <Cpu className='size-3' />
                      </div>
                      <div className='flex flex-col gap-0.5'>
                        <div className='flex items-center gap-1.5'>
                          <span className='font-semibold text-foreground'>
                            {item.algorithm?.name || subscription.algorithmId}
                          </span>
                          {item.algorithm?.version && (
                            <Badge
                              variant='outline'
                              className='h-4 px-1 text-[10px] text-muted-foreground'
                            >
                              v{item.algorithm.version}
                            </Badge>
                          )}
                        </div>
                        <div className='flex items-center gap-1.5 font-mono text-[11px] text-muted-foreground'>
                          <span title={subscription.id}>
                            {t('table.subscriptionId', '算法实例 ID')}:{' '}
                            {subscription.id.slice(0, 8)}...
                          </span>
                          <TaskCopyIdButton
                            id={subscription.id}
                            label='subscription'
                          />
                        </div>
                      </div>
                    </div>
                  </TableCell>

                  <TableCell className='py-2 font-mono text-xs font-medium text-foreground'>
                    {t('table.fpsValue', '{{fps}} FPS', {
                      fps: subscription.sampleFps,
                    })}
                  </TableCell>

                  <TableCell className='py-2 text-xs'>
                    <div className='flex flex-col'>
                      <span className='font-mono font-semibold text-foreground'>
                        {item.computePoints}{' '}
                        {t('channelCard.pointsSuffix', '点')}
                      </span>
                      <span className='font-mono text-[10px] text-muted-foreground'>
                        {costPerFrame} {t('dialog.costPerFrame', '点/帧')}
                      </span>
                    </div>
                  </TableCell>

                  <TableCell className='py-2 text-xs'>
                    <TaskStatusBadge
                      enabled={isItemEnabled}
                      isChannelEnabled={task.enabled}
                      algorithmStatus={item.algorithm?.status}
                      cameraHealth={group.camera.health}
                      cameraSession={group.camera.session}
                      isCameraEnabled={group.camera.enabled}
                      schedule={item.schedule}
                      scheduleJson={item.algorithmItem.scheduleJson}
                    />
                  </TableCell>

                  <TableCell className='py-2 text-xs'>
                    {hasRegion ? (
                      <div className='flex items-center gap-1.5'>
                        {region.roi.length > 0 && (
                          <Badge
                            variant='outline'
                            className={cn(
                              'gap-1 px-1.5 py-0 font-mono text-[10px] font-normal',
                              categoryBadgeClass(REGION_CATEGORY_HUE.roi)
                            )}
                          >
                            ROI: {region.roi.length}
                          </Badge>
                        )}
                        {region.mask.length > 0 && (
                          <Badge
                            variant='outline'
                            className={cn(
                              'gap-1 px-1.5 py-0 font-mono text-[10px] font-normal',
                              categoryBadgeClass(REGION_CATEGORY_HUE.mask)
                            )}
                          >
                            MASK: {region.mask.length}
                          </Badge>
                        )}
                      </div>
                    ) : (
                      <span className='text-[11px] text-muted-foreground'>
                        {t('region.notConfigured', '全画面上报')}
                      </span>
                    )}
                  </TableCell>

                  <TableCell className='py-2 text-end'>
                    <SubRowActions item={item} task={task} />
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}
