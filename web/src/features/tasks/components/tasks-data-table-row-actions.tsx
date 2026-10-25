import { type Row } from '@tanstack/react-table'
import { Copy, MoreHorizontal, Pencil, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { copyToClipboard } from '@/lib/clipboard'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { taskAlgorithmsPayload } from '../data/task-algorithm-payload'
import { type TaskRow } from '../data/task-row'
import { useUpdateTask } from '../hooks/use-tasks'
import { useTasksContext } from './tasks-provider'

interface TasksDataTableRowActionsProps {
  row: Row<TaskRow>
}

export function TasksDataTableRowActions({
  row,
}: TasksDataTableRowActionsProps) {
  const { t } = useTranslation('tasks')
  const { setOpen, setCurrentRow } = useTasksContext()
  const updateMutation = useUpdateTask()
  const { task, id, taskId } = row.original

  const handleCopy = async (value: string, message: string) => {
    // 非安全上下文下 navigator.clipboard 不存在，必须走共享工具的回退路径
    if (!(await copyToClipboard(value))) {
      toast.error(t('table.copyIdFailed', '复制失败，请手动复制'))
      return
    }
    toast.success(message)
  }

  /**
   * 删除动作有两种语义：通道只剩这一个算法时删整条任务，
   * 否则只从通道里摘掉这一条订阅（后端不支持单条订阅删除）。
   */
  const handleDelete = () => {
    if ((task.algorithms?.length ?? 0) <= 1) {
      setCurrentRow(task)
      setOpen('delete')
      return
    }

    updateMutation.mutate(
      {
        id: task.id,
        data: {
          algorithms: taskAlgorithmsPayload(task).filter(
            (algo) => algo.id !== id
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
          className='flex size-8 p-0 data-[state=open]:bg-muted'
          aria-label={t('table.actions', '操作')}
        >
          <MoreHorizontal className='size-4' />
          <span className='sr-only'>{t('table.actions', '操作')}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end' className='w-44'>
        <DropdownMenuItem
          onClick={() => {
            setCurrentRow(task)
            setOpen('channelTask')
          }}
        >
          <Pencil className='me-2 size-4' />
          {t('actions.edit', '编辑配置')}
        </DropdownMenuItem>

        <DropdownMenuItem
          onClick={() =>
            void handleCopy(
              taskId,
              t('table.copyTaskIdSuccess', '任务 ID 已复制到剪贴板')
            )
          }
        >
          <Copy className='me-2 size-4' />
          {t('table.copyTaskId', '复制任务 ID')}
        </DropdownMenuItem>

        <DropdownMenuItem
          onClick={() =>
            void handleCopy(
              id,
              t('table.copySubscriptionIdSuccess', '订阅 ID 已复制到剪贴板')
            )
          }
        >
          <Copy className='me-2 size-4' />
          {t('table.copySubscriptionId', '复制订阅 ID')}
        </DropdownMenuItem>

        <DropdownMenuSeparator />

        <DropdownMenuItem
          className='text-destructive focus:text-destructive'
          disabled={updateMutation.isPending}
          onClick={handleDelete}
        >
          <Trash2 className='me-2 size-4' />
          {t('actions.delete', '删除')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
