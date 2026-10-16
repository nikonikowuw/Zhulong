import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

interface RecordsPrimaryButtonsProps {
  onRefresh?: () => void
  isRefreshing?: boolean
}

export function RecordsPrimaryButtons({
  onRefresh,
  isRefreshing = false,
}: RecordsPrimaryButtonsProps) {
  const { t } = useTranslation('records')

  return (
    <div className='flex items-center gap-2'>
      <Button
        variant='outline'
        size='sm'
        className='h-8 gap-1.5'
        onClick={onRefresh}
        disabled={isRefreshing}
      >
        <RefreshCw
          className={cn('size-3.5', isRefreshing && 'animate-spin')}
        />
        <span>{t('filters.refresh', { defaultValue: '刷新' })}</span>
      </Button>
    </div>
  )
}
