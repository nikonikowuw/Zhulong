import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { type Task } from '../data/schema'
import { useDeleteTask } from '../hooks/use-tasks'

interface TaskDeleteDialogProps {
  task: Task | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function TaskDeleteDialog({
  task,
  open,
  onOpenChange,
}: TaskDeleteDialogProps): React.JSX.Element | null {
  const { t } = useTranslation('tasks')
  const deleteMutation = useDeleteTask()

  if (!task) return null

  const handleConfirm = () => {
    deleteMutation.mutate(task.id, {
      onSuccess: () => {
        toast.success(t('deleteDialog.success', '分析任务已成功删除'))
        onOpenChange(false)
      },
      onError: (err) => {
        toast.error(err.message || t('deleteDialog.error', '删除分析任务失败'))
      },
    })
  }

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('deleteDialog.title', '删除分析任务')}
      desc={t(
        'deleteDialog.desc',
        '确定要删除该分析任务吗？删除后系统将立即停止对该摄像头的视频帧订阅并释放所占算力。'
      )}
      confirmText={t('deleteDialog.confirm', '确认删除')}
      cancelBtnText={t('deleteDialog.cancel', '取消')}
      destructive
      isLoading={deleteMutation.isPending}
      handleConfirm={handleConfirm}
    />
  )
}
