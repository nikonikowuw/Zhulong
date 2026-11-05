import { useQueryClient } from '@tanstack/react-query'
import {
  FileSpreadsheet,
  FileUp,
  Images,
  Loader2,
  Plus,
  RefreshCw,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import {
  PERSONS_QUERY_KEY,
  useExportPersonsManifest,
} from '../hooks/use-persons'
import { usePersonsContext } from './persons-provider'

interface PersonsPrimaryButtonsProps {
  /** 当前 URL 搜索关键词，导出时保持一致，避免「看到的」和「导出的」不是同一批人。 */
  search?: string
  /** 当前底库筛选状态（enrolled / missing），同上。 */
  faceStatus?: string
}

export function PersonsPrimaryButtons({
  search,
  faceStatus,
}: PersonsPrimaryButtonsProps) {
  const { t } = useTranslation('persons')
  const { setOpen, setCurrentRow } = usePersonsContext()
  const queryClient = useQueryClient()
  const exportMutation = useExportPersonsManifest()

  const handleRefresh = () => {
    void queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })
  }

  const handleCreate = () => {
    setCurrentRow(null)
    setOpen('create')
  }

  const handleImport = () => {
    setCurrentRow(null)
    setOpen('import')
  }

  const handleImportFaces = () => {
    setCurrentRow(null)
    setOpen('import-faces')
  }

  const handleExportManifest = async () => {
    try {
      await exportMutation.mutateAsync({ search, faceStatus })
      toast.success(t('export.success', { defaultValue: '采像清单已导出' }))
    } catch {
      // 全局 API 错误处理已展示本地化服务端消息
    }
  }

  return (
    <div className='flex items-center gap-2'>
      <Button
        variant='outline'
        size='sm'
        className='h-8'
        onClick={handleRefresh}
        title={t('actions.refresh', { defaultValue: '刷新' })}
      >
        <RefreshCw className='me-1 h-4 w-4' />
        <span className='hidden sm:inline'>
          {t('actions.refresh', { defaultValue: '刷新' })}
        </span>
      </Button>

      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            variant='outline'
            size='sm'
            className='h-8'
            onClick={() => void handleExportManifest()}
            disabled={exportMutation.isPending}
            title={t('actions.exportManifest', {
              defaultValue: '导出采像清单',
            })}
          >
            {exportMutation.isPending ? (
              <Loader2 className='me-1 h-4 w-4 animate-spin' />
            ) : (
              <FileSpreadsheet className='me-1 h-4 w-4' />
            )}
            <span className='hidden md:inline'>
              {t('actions.exportManifest', { defaultValue: '导出采像清单' })}
            </span>
          </Button>
        </TooltipTrigger>
        <TooltipContent>
          <p>
            {t('export.tooltip', {
              defaultValue:
                '按当前搜索与底库筛选导出 CSV，含姓名、工号与建议照片文件名，可直接回传给「人脸批量导入」',
            })}
          </p>
        </TooltipContent>
      </Tooltip>

      <Button
        variant='outline'
        size='sm'
        className='h-8'
        onClick={handleImport}
        title={t('actions.import')}
      >
        <FileUp className='me-1 h-4 w-4' />
        <span>{t('actions.import')}</span>
      </Button>

      <Button
        variant='outline'
        size='sm'
        className='h-8'
        onClick={handleImportFaces}
        title={t('actions.importFaces', { defaultValue: '人脸批量导入' })}
      >
        <Images className='me-1 h-4 w-4' />
        <span>
          {t('actions.importFaces', { defaultValue: '人脸批量导入' })}
        </span>
      </Button>

      <Button size='sm' className='h-8' onClick={handleCreate}>
        <Plus className='me-1 h-4 w-4' />
        <span>{t('actions.create')}</span>
      </Button>
    </div>
  )
}
