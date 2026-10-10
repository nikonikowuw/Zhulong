import { useState } from 'react'
import { Trash2, AlertTriangle, CheckCircle2, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { storageApi } from '../api/storage-api'
import { formatBytes } from '../utils/format'
import { type CleanupSummary } from '../types'

interface ManualCleanupDialogProps {
  onSuccess?: (summary: CleanupSummary) => void
}

export function ManualCleanupDialog({ onSuccess }: ManualCleanupDialogProps) {
  const { t } = useTranslation('storage')
  const [open, setOpen] = useState(false)
  const [isLoading, setIsLoading] = useState(false)
  const [summary, setSummary] = useState<CleanupSummary | null>(null)
  const [error, setError] = useState<string | null>(null)

  const handleTrigger = async () => {
    setIsLoading(true)
    setError(null)
    setSummary(null)
    try {
      const res = await storageApi.triggerCleanup()
      setSummary(res)
      if (onSuccess) {
        onSuccess(res)
      }
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to trigger cleanup cycle'
      )
    } finally {
      setIsLoading(false)
    }
  }

  const handleOpenChange = (newOpen: boolean) => {
    if (!isLoading) {
      setOpen(newOpen)
      if (!newOpen) {
        setSummary(null)
        setError(null)
      }
    }
  }

  return (
    <AlertDialog open={open} onOpenChange={handleOpenChange}>
      <AlertDialogTrigger asChild>
        <Button variant='outline' size='sm' className='gap-1.5'>
          <Trash2 className='size-3.5 text-muted-foreground' />
          {t('actions.manual_cleanup', { defaultValue: '立即清理' })}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle className='flex items-center gap-2'>
            <AlertTriangle className='size-5 text-amber-500' />
            {t('dialog.title', { defaultValue: '触发磁盘空间清理' })}
          </AlertDialogTitle>
          <AlertDialogDescription className='space-y-2 text-left pt-1'>
            <p>
              {t('dialog.desc1', {
                defaultValue:
                  '系统将立即启动双水位回差引擎，依照阶梯优先级清理过期临时导出和最早的未加锁常规录像，直到磁盘使用率回落至低水位线以下。',
              })}
            </p>
            <p className='text-xs text-muted-foreground'>
              ⚠️{' '}
              {t('dialog.desc2', {
                defaultValue:
                  '所有带有 AI 告警事件标记或被手动加锁保护的录像与抓拍绝不会被提前清除。清理过程采用毫秒级批次让渡，不会造成前台录像掉帧。',
              })}
            </p>
          </AlertDialogDescription>
        </AlertDialogHeader>

        {summary && (
          <div className='rounded-md border border-emerald-500/20 bg-emerald-500/10 p-3 text-xs text-emerald-600 dark:text-emerald-400 space-y-1'>
            <div className='flex items-center gap-1.5 font-semibold'>
              <CheckCircle2 className='size-4' />
              {t('dialog.success_title', { defaultValue: '清理完成' })}
            </div>
            <div>
              {t('dialog.deleted_files', { defaultValue: '删除文件数' })}:{' '}
              {summary.deletedFiles}，
              {t('dialog.freed_space', { defaultValue: '释放空间' })}:{' '}
              {formatBytes(summary.freedBytes)}，
              {t('dialog.duration', { defaultValue: '耗时' })}:{' '}
              {summary.durationMs} ms
            </div>
          </div>
        )}

        {error && (
          <div className='rounded-md border border-destructive/20 bg-destructive/10 p-3 text-xs text-destructive'>
            {error}
          </div>
        )}

        <AlertDialogFooter>
          {summary ? (
            <Button size='sm' onClick={() => handleOpenChange(false)}>
              {t('dialog.close', { defaultValue: '完成' })}
            </Button>
          ) : (
            <>
              <AlertDialogCancel disabled={isLoading}>
                {t('dialog.cancel', { defaultValue: '取消' })}
              </AlertDialogCancel>
              <AlertDialogAction
                onClick={(e) => {
                  e.preventDefault()
                  void handleTrigger()
                }}
                disabled={isLoading}
                className='bg-destructive hover:bg-destructive/90 text-destructive-foreground'
              >
                {isLoading ? (
                  <>
                    <Loader2 className='mr-1.5 size-3.5 animate-spin' />
                    {t('dialog.cleaning', { defaultValue: '正在清理...' })}
                  </>
                ) : (
                  t('dialog.confirm', { defaultValue: '确认执行' })
                )}
              </AlertDialogAction>
            </>
          )}
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
