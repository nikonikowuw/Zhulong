import { AlertCircle, CheckCircle2, Loader2, XCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  STATUS_LEVEL_CLASS,
  statusDotClass,
  statusLevelClass,
  statusTextClass,
  type StatusLevel,
} from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { type FaceImportJobStatus } from '../data/schema'

const JOB_STATUS_LEVEL: Record<FaceImportJobStatus, StatusLevel> = {
  PENDING: 'warning',
  EXTRACTING: 'warning',
  PROCESSING: 'warning',
  COMPLETED: 'active',
  FAILED: 'destructive',
  CANCELLED: 'inactive',
}

interface FaceJobProgressData {
  status: FaceImportJobStatus
  total: number
  processed: number
  succeeded: number
  failed: number
  currentFile?: string
  errorMessage?: string
}

interface FaceJobProgressPanelProps {
  job: FaceJobProgressData | null
  isBusy: boolean
  isQueryError: boolean
  onRetryQuery: () => void
  statusLabel: string
  statusBadgeLabel: string
  extraBadge?: string
  progressLabel: string
  currentFileLabel: string
  statLabels: {
    total: string
    processed: string
    succeeded: string
    failed: string
  }
  alerts: {
    errorTitle: string
    unknownError: string
    completeTitle: string
    completeSummary: string
    cancelledTitle?: string
    cancelledSummary?: string
  }
}

export function FaceJobProgressPanel({
  job,
  isBusy,
  isQueryError,
  onRetryQuery,
  statusLabel,
  statusBadgeLabel,
  extraBadge,
  progressLabel,
  currentFileLabel,
  statLabels,
  alerts,
}: FaceJobProgressPanelProps) {
  const { t } = useTranslation('persons')

  if (isQueryError) {
    return (
      <div className='flex min-w-0 flex-col items-center justify-center gap-2 py-8 text-center text-destructive'>
        <AlertCircle className='size-6' />
        <span className='font-medium'>{t('table.loadFailed')}</span>
        <Button
          type='button'
          size='sm'
          variant='outline'
          onClick={onRetryQuery}
          className='mt-1'
        >
          {t('actions.retry')}
        </Button>
      </div>
    )
  }

  const total = job?.total ?? 0
  const processed = job?.processed ?? 0
  const succeeded = job?.succeeded ?? 0
  const failed = job?.failed ?? 0
  const percent =
    total > 0 ? Math.min(100, Math.round((processed / total) * 100)) : 0
  const statusLevel = JOB_STATUS_LEVEL[job?.status ?? 'PENDING']

  return (
    <div className='min-w-0 space-y-4 py-2'>
      <div className='flex min-w-0 flex-wrap items-center justify-between gap-2'>
        <div className='flex min-w-0 flex-1 items-center gap-2'>
          {isBusy ? (
            <Loader2 className='size-5 shrink-0 animate-spin text-primary' />
          ) : job?.status === 'COMPLETED' ? (
            <CheckCircle2
              className={cn('size-5 shrink-0', statusTextClass('active'))}
            />
          ) : (
            <XCircle className='size-5 shrink-0 text-destructive' />
          )}
          <span className='truncate font-medium'>{statusLabel}</span>
        </div>
        <div className='flex shrink-0 items-center gap-1.5'>
          {extraBadge && <Badge variant='outline'>{extraBadge}</Badge>}
          <Badge
            variant='outline'
            className={cn('gap-1.5', statusLevelClass(statusLevel))}
          >
            <span
              className={cn(
                'inline-block h-1.5 w-1.5 rounded-full',
                statusDotClass(statusLevel),
                isBusy && 'animate-pulse'
              )}
            />
            {statusBadgeLabel}
          </Badge>
        </div>
      </div>

      <div className='min-w-0 space-y-1.5'>
        <div className='flex items-center justify-between gap-2 text-xs text-muted-foreground'>
          <span>{progressLabel}</span>
          <span className='tabular-nums'>{percent}%</span>
        </div>
        <div
          role='progressbar'
          aria-valuenow={percent}
          aria-valuemin={0}
          aria-valuemax={100}
          className='h-2 w-full overflow-hidden rounded-full bg-secondary'
        >
          <div
            className='h-full bg-primary transition-all duration-300'
            style={{ width: `${percent}%` }}
          />
        </div>
        {job?.currentFile && isBusy && (
          <p
            className='min-w-0 truncate text-xs text-muted-foreground'
            title={job.currentFile}
          >
            {currentFileLabel} {job.currentFile}
          </p>
        )}
      </div>

      <div className='grid min-w-0 grid-cols-2 gap-2 text-center sm:grid-cols-4'>
        <div className='min-w-0 rounded-md border border-border bg-muted/20 p-2'>
          <div className='truncate text-xs text-muted-foreground'>
            {statLabels.total}
          </div>
          <div className='text-lg font-semibold tabular-nums'>{total}</div>
        </div>
        <div className='min-w-0 rounded-md border border-border bg-muted/20 p-2'>
          <div className='truncate text-xs text-muted-foreground'>
            {statLabels.processed}
          </div>
          <div className='text-lg font-semibold tabular-nums'>{processed}</div>
        </div>
        <div
          className={cn(
            'min-w-0 rounded-md border p-2',
            STATUS_LEVEL_CLASS.active
          )}
        >
          <div className='truncate text-xs'>{statLabels.succeeded}</div>
          <div className='text-lg font-semibold tabular-nums'>{succeeded}</div>
        </div>
        <div
          className={cn(
            'min-w-0 rounded-md border p-2',
            STATUS_LEVEL_CLASS.destructive
          )}
        >
          <div className='truncate text-xs'>{statLabels.failed}</div>
          <div className='text-lg font-semibold tabular-nums'>{failed}</div>
        </div>
      </div>

      {job?.status === 'FAILED' && (
        <Alert variant='destructive'>
          <AlertCircle className='size-4' />
          <AlertTitle>{alerts.errorTitle}</AlertTitle>
          <AlertDescription className='break-words'>
            {job.errorMessage || alerts.unknownError}
          </AlertDescription>
        </Alert>
      )}

      {job?.status === 'COMPLETED' && (
        <Alert className={cn('border', STATUS_LEVEL_CLASS.active)}>
          <CheckCircle2 className='size-4' />
          <AlertTitle>{alerts.completeTitle}</AlertTitle>
          <AlertDescription>{alerts.completeSummary}</AlertDescription>
        </Alert>
      )}

      {job?.status === 'CANCELLED' && alerts.cancelledTitle && (
        <Alert>
          <AlertCircle className='size-4' />
          <AlertTitle>{alerts.cancelledTitle}</AlertTitle>
          <AlertDescription>{alerts.cancelledSummary}</AlertDescription>
        </Alert>
      )}
    </div>
  )
}
