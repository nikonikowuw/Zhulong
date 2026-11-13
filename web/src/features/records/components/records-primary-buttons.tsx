import { Download, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'

interface RecordsPrimaryButtonsProps {
  onRefresh?: () => void
  isRefreshing?: boolean
  onExport?: () => void
}

export function RecordsPrimaryButtons({
  onRefresh,
  isRefreshing = false,
  onExport,
}: RecordsPrimaryButtonsProps) {
  const { t } = useTranslation('records')

  return (
    <div className='flex items-center gap-2'>
      <Button
        variant='outline'
        size='sm'
        className='h-8 gap-1.5'
        onClick={onExport}
      >
        <Download className='size-3.5' />
        <span>{t('export.button')}</span>
      </Button>

      <Button
        variant='outline'
        size='sm'
        className='h-8 gap-1.5'
        onClick={onRefresh}
        disabled={isRefreshing}
      >
        <RefreshCw className={cn('size-3.5', isRefreshing && 'animate-spin')} />
        <span>{t('filters.refresh')}</span>
      </Button>
    </div>
  )
}
