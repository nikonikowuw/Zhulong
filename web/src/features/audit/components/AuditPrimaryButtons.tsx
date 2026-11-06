import { Download, Loader2, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { type GetAuditLogsParams } from '../data/schema'
import { useAuditExport } from '../hooks/use-audit-export'

interface AuditPrimaryButtonsProps {
  onRefresh?: () => void
  isRefreshing?: boolean
  /**
   * 已归一化的列表筛选条件（`index.tsx` 里由路由 search 换算而来）。
   * 导出会忽略其中的 `page` / `pageSize`，自己按服务端上限翻页。
   */
  filter?: GetAuditLogsParams
}

export function AuditPrimaryButtons({
  onRefresh,
  isRefreshing,
  filter = {},
}: AuditPrimaryButtonsProps) {
  const { t } = useTranslation('audit')
  const { exportLogs, isExporting } = useAuditExport()

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
        className='h-9 gap-1.5'
        onClick={() => exportLogs(filter)}
        disabled={isExporting}
      >
        {isExporting ? (
          <Loader2 className='h-4 w-4 animate-spin' />
        ) : (
          <Download className='h-4 w-4' />
        )}
        <span>{t('actions.exportLogs')}</span>
      </Button>
    </div>
  )
}
