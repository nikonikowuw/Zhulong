import { RefreshCw, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { useAuditContext } from './AuditProvider'

interface AuditPrimaryButtonsProps {
  onRefresh?: () => void
  isRefreshing?: boolean
}

export function AuditPrimaryButtons({
  onRefresh,
  isRefreshing,
}: AuditPrimaryButtonsProps) {
  const { t } = useTranslation('audit')
  const { openClear } = useAuditContext()

  return (
    <div className='flex items-center gap-2'>
      <Button
        variant='outline'
        size='sm'
        className='h-9 gap-1.5'
        onClick={onRefresh}
        disabled={isRefreshing}
      >
        <RefreshCw
          className={`h-4 w-4 ${isRefreshing ? 'animate-spin' : ''}`}
        />
        <span>{t('actions.refresh')}</span>
      </Button>

      <Button
        variant='outline'
        size='sm'
        className='h-9 gap-1.5 border-destructive/30 text-destructive hover:bg-destructive/10 hover:text-destructive'
        onClick={openClear}
      >
        <Trash2 className='h-4 w-4' />
        <span>{t('actions.clearLogs')}</span>
      </Button>
    </div>
  )
}
