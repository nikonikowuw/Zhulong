import { useState } from 'react'
import { type Table } from '@tanstack/react-table'
import {
  Activity,
  CheckCircle2,
  SlidersHorizontal,
  Trash2,
  XCircle,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { DataTableBulkActions as BulkActionsToolbar } from '@/components/data-table'
import { type Camera } from '../data/schema'
import { useDiagnoseCamera, useToggleCameraEnabled } from '../hooks/use-cameras'
import { CamerasMultiDeleteDialog } from './cameras-multi-delete-dialog'

type DataTableBulkActionsProps = {
  table: Table<Camera>
}

export function CamerasDataTableBulkActions({
  table,
}: DataTableBulkActionsProps) {
  const { t } = useTranslation('cameras')
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false)
  const [isUpdating, setIsUpdating] = useState(false)

  const toggleMutation = useToggleCameraEnabled()
  const diagnoseMutation = useDiagnoseCamera()

  const selectedRows = table.getFilteredSelectedRowModel().rows
  const selectedCameras = selectedRows.map((row) => row.original)

  if (selectedRows.length === 0) return null

  // 批量切换启用/停用状态
  const handleBulkStatusChange = async (enabled: boolean) => {
    setIsUpdating(true)
    const toastId = toast.loading(t('toast.batchStatusUpdating'))
    try {
      await Promise.all(
        selectedCameras.map((camera) =>
          toggleMutation.mutateAsync({
            camera,
            enabled,
          })
        )
      )
      toast.success(
        t('toast.batchStatusUpdated', { count: selectedCameras.length }),
        { id: toastId }
      )
      table.resetRowSelection()
    } catch {
      toast.error('Batch status update failed', { id: toastId })
    } finally {
      setIsUpdating(false)
    }
  }

  // 批量触发连通性探针诊断
  const handleBulkDiagnose = async () => {
    setIsUpdating(true)
    const toastId = toast.loading(
      t('toast.batchDiagnosing', { count: selectedCameras.length })
    )
    try {
      await Promise.all(
        selectedCameras.map((camera) => diagnoseMutation.mutateAsync(camera.id))
      )
      toast.success(
        t('toast.batchDiagnoseSuccess', { count: selectedCameras.length }),
        { id: toastId }
      )
    } catch {
      toast.error('Batch diagnose probe failed', { id: toastId })
    } finally {
      setIsUpdating(false)
    }
  }

  return (
    <>
      <BulkActionsToolbar table={table} entityName='camera'>
        {/* 1. 状态变更下拉菜单 (对齐 shadcn-admin tasks 模式) */}
        <DropdownMenu>
          <Tooltip>
            <TooltipTrigger asChild>
              <DropdownMenuTrigger asChild>
                <Button
                  variant='outline'
                  size='icon'
                  className='size-8'
                  disabled={isUpdating}
                  aria-label={t('table.batchUpdateStatus')}
                  title={t('table.batchUpdateStatus')}
                >
                  <SlidersHorizontal />
                  <span className='sr-only'>
                    {t('table.batchUpdateStatus')}
                  </span>
                </Button>
              </DropdownMenuTrigger>
            </TooltipTrigger>
            <TooltipContent>
              <p>{t('table.batchUpdateStatus')}</p>
            </TooltipContent>
          </Tooltip>
          <DropdownMenuContent sideOffset={14}>
            <DropdownMenuItem
              onClick={() => void handleBulkStatusChange(true)}
              className='cursor-pointer'
            >
              <CheckCircle2 className='mr-2 size-4 text-emerald-500' />
              <span>{t('table.batchEnable')}</span>
            </DropdownMenuItem>
            <DropdownMenuItem
              onClick={() => void handleBulkStatusChange(false)}
              className='cursor-pointer'
            >
              <XCircle className='mr-2 size-4 text-muted-foreground' />
              <span>{t('table.batchDisable')}</span>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>

        {/* 2. 批量连通性诊断探针 */}
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant='outline'
              size='icon'
              className='size-8'
              disabled={isUpdating}
              onClick={() => void handleBulkDiagnose()}
              aria-label={t('table.batchDiagnose')}
              title={t('table.batchDiagnose')}
            >
              <Activity
                className={diagnoseMutation.isPending ? 'animate-pulse' : ''}
              />
              <span className='sr-only'>{t('table.batchDiagnose')}</span>
            </Button>
          </TooltipTrigger>
          <TooltipContent>
            <p>{t('table.batchDiagnose')}</p>
          </TooltipContent>
        </Tooltip>

        {/* 3. 批量删除 (对齐 shadcn-admin 高危确认弹窗) */}
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant='destructive'
              size='icon'
              className='size-8'
              disabled={isUpdating}
              onClick={() => setShowDeleteConfirm(true)}
              aria-label={t('table.batchDelete')}
              title={t('table.batchDelete')}
            >
              <Trash2 />
              <span className='sr-only'>{t('table.batchDelete')}</span>
            </Button>
          </TooltipTrigger>
          <TooltipContent>
            <p>{t('table.batchDelete')}</p>
          </TooltipContent>
        </Tooltip>
      </BulkActionsToolbar>

      <CamerasMultiDeleteDialog
        open={showDeleteConfirm}
        onOpenChange={setShowDeleteConfirm}
        table={table}
      />
    </>
  )
}
