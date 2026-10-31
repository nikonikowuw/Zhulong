import { useQueryClient } from '@tanstack/react-query'
import { RefreshCw, UploadCloud } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { ALGORITHMS_QUERY_KEY } from '../hooks/use-algorithms'
import { useAlgorithmsContext } from './algorithms-provider'

export function AlgorithmsPrimaryButtons(): React.JSX.Element {
  const { t } = useTranslation('algorithms')
  const { setOpen, setCurrentRow } = useAlgorithmsContext()
  const queryClient = useQueryClient()

  const handleRefresh = () => {
    void queryClient.invalidateQueries({ queryKey: ALGORITHMS_QUERY_KEY })
  }

  const handleUpload = () => {
    setCurrentRow(null)
    setOpen('upload')
  }

  return (
    <div className='flex items-center gap-2'>
      <Button
        variant='outline'
        size='sm'
        className='h-8'
        onClick={handleRefresh}
        title={t('retry', '刷新')}
      >
        <RefreshCw className='me-1 size-4' />
        <span className='hidden sm:inline'>{t('refresh', '刷新')}</span>
      </Button>

      <Button size='sm' className='h-8 gap-2 shadow-xs' onClick={handleUpload}>
        <UploadCloud className='size-4' />
        <span>{t('uploadButton', '安装算法包')}</span>
      </Button>
    </div>
  )
}
