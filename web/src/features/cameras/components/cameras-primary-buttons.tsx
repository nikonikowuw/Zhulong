import { useQueryClient } from '@tanstack/react-query'
import { Plus, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { CAMERAS_QUERY_KEY } from '../hooks/use-cameras'
import { useCamerasContext } from './cameras-provider'

export function CamerasPrimaryButtons() {
  const { t } = useTranslation('cameras')
  const { setOpen, setCurrentRow } = useCamerasContext()
  const queryClient = useQueryClient()

  const handleRefresh = () => {
    void queryClient.invalidateQueries({ queryKey: CAMERAS_QUERY_KEY })
  }

  const handleCreate = () => {
    setCurrentRow(null)
    setOpen('create')
  }

  return (
    <div className='flex items-center gap-2'>
      <Button
        variant='outline'
        size='sm'
        className='h-8'
        onClick={handleRefresh}
        title={t('actions.refresh')}
      >
        <RefreshCw className='me-1 h-4 w-4' />
        <span className='hidden sm:inline'>{t('actions.refresh')}</span>
      </Button>

      <Button size='sm' className='h-8' onClick={handleCreate}>
        <Plus className='me-1 h-4 w-4' />
        <span>{t('actions.create')}</span>
      </Button>
    </div>
  )
}
