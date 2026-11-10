import { useQueryClient } from '@tanstack/react-query'
import { Plus, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { TASKS_QUERY_KEY } from '../hooks/use-tasks'
import { useTasksActions } from './tasks-provider'

export function TasksPrimaryButtons() {
  const { t } = useTranslation('tasks')
  const { setOpen, setCurrentRow, setTargetCameraId } = useTasksActions()
  const queryClient = useQueryClient()

  const handleRefresh = () => {
    void queryClient.invalidateQueries({ queryKey: TASKS_QUERY_KEY })
  }

  const handleCreate = () => {
    setCurrentRow(null)
    setTargetCameraId(null)
    setOpen('channelTask')
  }

  return (
    <div className='flex items-center gap-2'>
      <Button
        variant='outline'
        size='sm'
        className='h-8'
        onClick={handleRefresh}
        title={t('actions.refresh', '刷新')}
      >
        <RefreshCw className='me-1 h-4 w-4' />
        <span className='hidden sm:inline'>{t('actions.refresh', '刷新')}</span>
      </Button>

      <Button size='sm' className='h-8' onClick={handleCreate}>
        <Plus className='me-1 h-4 w-4' />
        <span>{t('newChannelTask', '配置通道任务')}</span>
      </Button>
    </div>
  )
}
