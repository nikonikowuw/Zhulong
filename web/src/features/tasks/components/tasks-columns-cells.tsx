import { useState } from 'react'
import { type Column, type Row } from '@tanstack/react-table'
import { Check, Copy } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { copyToClipboard } from '@/lib/clipboard'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { DataTableColumnHeader } from '@/components/data-table'
import { taskAlgorithmsPayload } from '../data/task-algorithm-payload'
import { type TaskRow } from '../data/task-row'
import { useUpdateTask } from '../hooks/use-tasks'
import { TaskStatusBadge } from './task-status-badge'

export function TaskColumnHeader({
  column,
  titleKey,
}: {
  column: Column<TaskRow, unknown>
  titleKey: string
}) {
  const { t } = useTranslation('tasks')
  return <DataTableColumnHeader column={column} title={t(titleKey)} />
}

export function TaskActionsHeader() {
  const { t } = useTranslation('tasks')
  return <span className='sr-only'>{t('table.actions')}</span>
}

/** 单条 ID 的内联复制控件（非安全上下文下由共享工具回退）。 */
function CopyIdButton({
  id,
  label,
}: {
  id: string
  label: string
}): React.JSX.Element {
  const { t } = useTranslation('tasks')
  const [copied, setCopied] = useState(false)

  const handleCopy = async (event: React.MouseEvent) => {
    event.stopPropagation()
    if (!(await copyToClipboard(id))) {
      toast.error(t('table.copyIdFailed', '复制失败，请手动复制'))
      return
    }
    setCopied(true)
    toast.success(
      label === 'task'
        ? t('table.copyTaskIdSuccess', '任务 ID 已复制到剪贴板')
        : t('table.copySubscriptionIdSuccess', '订阅 ID 已复制到剪贴板')
    )
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <Button
      variant='ghost'
      size='icon'
      className='size-6 shrink-0 text-muted-foreground opacity-60 transition-opacity group-hover:opacity-100 hover:text-foreground'
      aria-label={
        label === 'task'
          ? t('table.copyTaskId', '复制任务 ID')
          : t('table.copySubscriptionId', '复制订阅 ID')
      }
      title={
        label === 'task'
          ? t('table.copyTaskId', '复制任务 ID')
          : t('table.copySubscriptionId', '复制订阅 ID')
      }
      onClick={handleCopy}
    >
      {copied ? (
        <Check className='size-3.5 text-emerald-500' />
      ) : (
        <Copy className='size-3.5' />
      )}
    </Button>
  )
}

/**
 * 任务 / 订阅 ID 双行单元格。
 *
 * 本表按「通道任务 × 算法订阅」拍平，主键语义有两层，因此两层都展示：
 * 首行 `analysis_tasks.id`（`/tasks/{id}` 与审计日志用的就是它），
 * 次行 `task_algorithms.id`（update payload 里的 `algorithms[].id`）。
 */
export function TaskIdsCell({ row }: { row: Row<TaskRow> }) {
  const taskId = row.original.taskId
  const subId = row.original.id

  return (
    <div className='flex flex-col gap-0.5 font-mono text-xs'>
      <div className='flex items-center gap-1.5'>
        <span className='text-foreground'>{taskId.slice(0, 8)}...</span>
        <CopyIdButton id={taskId} label='task' />
      </div>
      <div className='flex items-center gap-1.5 text-muted-foreground'>
        <span>{subId.slice(0, 8)}...</span>
        <CopyIdButton id={subId} label='subscription' />
      </div>
    </div>
  )
}

export function TaskCameraCell({ row }: { row: Row<TaskRow> }) {
  return <span className='text-xs font-medium'>{row.original.cameraName}</span>
}

export function TaskAlgorithmCell({ row }: { row: Row<TaskRow> }) {
  const { t } = useTranslation('tasks')
  return (
    <div className='flex flex-col'>
      <span className='text-xs font-medium'>{row.original.algorithmName}</span>
      <span className='text-[11px] text-muted-foreground'>
        {row.original.computeCost} {t('dialog.costPerFrame', '点/帧')}
      </span>
    </div>
  )
}

export function TaskFpsCell({ row }: { row: Row<TaskRow> }) {
  return (
    <span className='text-xs font-medium'>{row.original.sampleFps} FPS</span>
  )
}

export function TaskPointsCell({ row }: { row: Row<TaskRow> }) {
  const { t } = useTranslation('tasks')
  return (
    <span className='font-mono text-xs font-medium'>
      {row.original.computePoints} {t('channelCard.pointsSuffix', '点')}
    </span>
  )
}

/**
 * 状态列：行内开关 + 四级状态徽章。
 * 与 Cameras 的 `CameraSwitchCell` 一致，mutation 收敛在单元格内，
 * 列定义保持纯静态（不允许在 columns 模块里塞 hooks）。
 */
export function TaskStatusCell({ row }: { row: Row<TaskRow> }) {
  const { t } = useTranslation('tasks')
  const updateMutation = useUpdateTask()
  const { task, id, isAlgoEnabled, enabled } = row.original

  return (
    <div className='flex items-center gap-2'>
      <Switch
        checked={enabled}
        disabled={updateMutation.isPending}
        onCheckedChange={(checked) => {
          updateMutation.mutate(
            {
              id: task.id,
              data: {
                algorithms: taskAlgorithmsPayload(task, {
                  id,
                  enabled: checked,
                }),
              },
            },
            {
              onSuccess: () => {
                toast.success(t('table.toggleSuccess', '任务状态已更新'))
              },
              onError: () => {
                toast.error(t('table.toggleError', '更新任务状态失败'))
              },
            }
          )
        }}
        aria-label={t('channelCard.toggleChannel', '切换通道分析状态')}
        data-state={isAlgoEnabled ? 'checked' : 'unchecked'}
      />
      <TaskStatusBadge
        enabled={enabled}
        isChannelEnabled={row.original.isChannelEnabled}
        algorithmStatus={row.original.algorithm?.status}
      />
    </div>
  )
}

/** 更新时间：遵循 §3.4，用 Intl 按当前语言与时区格式化。 */
export function TaskUpdatedAtCell({ row }: { row: Row<TaskRow> }) {
  const raw = row.original.updatedAt
  if (!raw) return <span className='text-xs text-muted-foreground'>-</span>
  return (
    <span className='text-xs whitespace-nowrap text-muted-foreground'>
      {new Date(raw).toLocaleString()}
    </span>
  )
}
