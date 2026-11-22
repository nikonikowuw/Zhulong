import { useState } from 'react'
import { ArrowRightLeft, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { statusLevelClass, type StatusLevel } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  useLatestStorageMigration,
  useStartStorageMigration,
} from '../hooks/use-storage'
import { type MigrationJobStatus } from '../types'

interface StorageMigrationCardProps {
  enabled: boolean
}

const JOB_STATUS_BADGE: Record<
  MigrationJobStatus,
  { level: StatusLevel; key: string }
> = {
  PENDING: { level: 'warning', key: 'migration.status_pending' },
  EXTRACTING: { level: 'warning', key: 'migration.status_extracting' },
  PROCESSING: { level: 'warning', key: 'migration.status_processing' },
  COMPLETED: { level: 'active', key: 'migration.status_completed' },
  FAILED: { level: 'destructive', key: 'migration.status_failed' },
  CANCELLED: { level: 'inactive', key: 'migration.status_cancelled' },
}

export function StorageMigrationCard({ enabled }: StorageMigrationCardProps) {
  const { t } = useTranslation('storage')
  const [deleteLocal, setDeleteLocal] = useState(false)
  const { data: latestJob } = useLatestStorageMigration(enabled)
  const startMutation = useStartStorageMigration()

  if (!enabled) {
    return null
  }

  const isJobRunning =
    latestJob?.status === 'PENDING' ||
    latestJob?.status === 'EXTRACTING' ||
    latestJob?.status === 'PROCESSING'

  const handleStart = async () => {
    try {
      await startMutation.mutateAsync({ deleteLocal })
      toast.success(
        t('migration.started', { defaultValue: '后台历史资产迁移任务已提交。' })
      )
    } catch {
      // 错误提示由 apiClient 全局拦截器统一处理
    }
  }

  const statusMeta = latestJob ? JOB_STATUS_BADGE[latestJob.status] : null

  return (
    <Card className='border-border/60 bg-card/60 backdrop-blur-sm'>
      <CardHeader className='pb-3'>
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <div className='flex items-center gap-2'>
            <ArrowRightLeft className='size-4 text-primary' />
            <CardTitle className='text-base font-semibold'>
              {t('migration.title', { defaultValue: '本地历史资产迁移到 S3' })}
            </CardTitle>
          </div>
          {latestJob && statusMeta && (
            <Badge
              variant='outline'
              className={cn(statusLevelClass(statusMeta.level))}
            >
              {t(statusMeta.key)}
            </Badge>
          )}
        </div>
      </CardHeader>

      <CardContent className='space-y-4'>
        <p className='text-xs text-muted-foreground'>{t('migration.desc')}</p>

        <div className='flex flex-wrap items-center justify-between gap-3 rounded-md border border-border/60 bg-muted/20 p-3'>
          <div className='flex items-center gap-3'>
            <Switch
              id='storage-migration-delete-local'
              checked={deleteLocal}
              disabled={isJobRunning || startMutation.isPending}
              onCheckedChange={setDeleteLocal}
            />
            <div className='space-y-0.5'>
              <Label
                htmlFor='storage-migration-delete-local'
                className='text-sm font-medium'
              >
                {t('migration.delete_local', {
                  defaultValue: '迁移校验成功后删除本地源文件',
                })}
              </Label>
              <p className='text-xs text-muted-foreground'>
                {t('migration.delete_local_desc')}
              </p>
            </div>
          </div>

          <Button
            type='button'
            size='sm'
            variant='secondary'
            onClick={() => void handleStart()}
            disabled={isJobRunning || startMutation.isPending}
            className='gap-1.5'
          >
            {isJobRunning || startMutation.isPending ? (
              <Loader2 className='size-3.5 animate-spin' />
            ) : (
              <ArrowRightLeft className='size-3.5' />
            )}
            {t('migration.btn_start', { defaultValue: '开始历史资产迁移' })}
          </Button>
        </div>

        {latestJob && (
          <div className='space-y-2 text-xs'>
            <div className='h-2 w-full overflow-hidden rounded-full bg-secondary'>
              <div
                style={{
                  width: `${Math.min(100, Math.max(0, latestJob.progress))}%`,
                }}
                className='h-full bg-primary transition-all duration-300'
              />
            </div>
            <div className='flex flex-wrap items-center justify-between gap-2 text-muted-foreground'>
              <span>
                {t('migration.summary', {
                  processed: latestJob.processed,
                  total: latestJob.total,
                  succeeded: latestJob.succeeded,
                  failed: latestJob.failed,
                })}
              </span>
              {latestJob.currentFile && (
                <span className='font-mono'>
                  {t('migration.current_file', {
                    file: latestJob.currentFile,
                  })}
                </span>
              )}
            </div>
            {latestJob.errorMessage && (
              <p className='font-mono text-destructive'>
                {latestJob.errorMessage}
              </p>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
