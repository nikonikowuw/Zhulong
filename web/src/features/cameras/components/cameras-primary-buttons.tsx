import { useQueryClient } from '@tanstack/react-query'
import { Plus, Radar, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { CAMERAS_QUERY_KEY } from '../hooks/use-cameras'
import { useCamerasActions } from './cameras-provider'

export function CamerasPrimaryButtons() {
  const { t } = useTranslation('cameras')
  const { setOpen, setCurrentRow } = useCamerasActions()
  const queryClient = useQueryClient()

  const handleRefresh = () => {
    void queryClient.invalidateQueries({ queryKey: CAMERAS_QUERY_KEY })
  }

  const handleCreate = () => {
    setCurrentRow(null)
    setOpen('create')
  }

  const handleDiscover = () => {
    setCurrentRow(null)
    setOpen('discover')
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
        <RefreshCw className='me-1 size-4' />
        <span className='hidden sm:inline'>{t('actions.refresh')}</span>
      </Button>

      <Button
        variant='outline'
        size='sm'
        className='h-8'
        onClick={handleDiscover}
        title={t('actions.discover')}
      >
        <Radar className='me-1 size-4' />
        <span>{t('actions.discover')}</span>
      </Button>

      <Button size='sm' className='h-8' onClick={handleCreate}>
        <Plus className='me-1 size-4' />
        <span>{t('actions.create')}</span>
      </Button>
    </div>
  )
}
