import { memo } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import {
  FileSpreadsheet,
  FileUp,
  Images,
  Loader2,
  Plus,
  RefreshCw,
  ScanFace,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { errorMessageOf } from '@/lib/handle-server-error'
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
import { usePersonsActions, usePersonsState } from './persons-provider'

interface PersonsPrimaryButtonsProps {
  /** 当前 URL 搜索关键词，导出时保持一致，避免「看到的」和「导出的」不是同一批人。 */
  search?: string
  /** 当前底库筛选状态（enrolled / missing），同上。 */
  faceStatus?: string
}

export const PersonsPrimaryButtons = memo(function PersonsPrimaryButtons({
  search,
  faceStatus,
}: PersonsPrimaryButtonsProps) {
  const { t } = useTranslation('persons')
  const { isFaceReextractRunning } = usePersonsState()
  const { setOpen, setCurrentRow, openFaceReextract } = usePersonsActions()
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

  const handleReextractFaces = () => {
    setCurrentRow(null)
    openFaceReextract(null)
  }

  const handleExportManifest = async () => {
    try {
      await exportMutation.mutateAsync({ search, faceStatus })
      toast.success(t('export.success'))
    } catch (err: unknown) {
      // exportManifest 会把 blob 形态的 JSON 错误体还原成 Error；
      // 这里只补一次提示，避免用户在按钮无反应时反复点。
      // 拦截器已把 blob 错误体里的服务端消息抬到 error.message。
      toast.error(errorMessageOf(err))
    }
  }

  return (
    <div className='flex flex-wrap items-center gap-2'>
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

      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            variant='outline'
            size='sm'
            className='h-8'
            onClick={() => void handleExportManifest()}
            disabled={exportMutation.isPending}
            title={t('actions.exportManifest')}
          >
            {exportMutation.isPending ? (
              <Loader2 className='me-1 h-4 w-4 animate-spin' />
            ) : (
              <FileSpreadsheet className='me-1 h-4 w-4' />
            )}
            <span className='hidden md:inline'>
              {t('actions.exportManifest')}
            </span>
          </Button>
        </TooltipTrigger>
        <TooltipContent>
          <p className='max-w-xs'>{t('export.tooltip')}</p>
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
        <span className='hidden sm:inline'>{t('actions.import')}</span>
      </Button>

      <Button
        variant='outline'
        size='sm'
        className='h-8'
        onClick={handleImportFaces}
        title={t('actions.importFaces')}
      >
        <Images className='me-1 h-4 w-4' />
        <span className='hidden md:inline'>{t('actions.importFaces')}</span>
      </Button>

      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            variant='outline'
            size='sm'
            className='h-8'
            onClick={handleReextractFaces}
            title={t('actions.reextractFeatures')}
          >
            {isFaceReextractRunning ? (
              <Loader2 className='me-1 h-4 w-4 animate-spin text-primary' />
            ) : (
              <ScanFace className='me-1 h-4 w-4' />
            )}
            <span className='hidden lg:inline'>
              {t('actions.reextractFeatures')}
            </span>
          </Button>
        </TooltipTrigger>
        <TooltipContent>
          <p className='max-w-xs'>{t('faceReextract.toolbarTooltip')}</p>
        </TooltipContent>
      </Tooltip>

      <Button size='sm' className='h-8' onClick={handleCreate}>
        <Plus className='me-1 h-4 w-4' />
        <span>{t('actions.create')}</span>
      </Button>
    </div>
  )
})
