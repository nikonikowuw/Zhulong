import { useRef } from 'react'
import {
  AlertCircle,
  CheckCircle2,
  Download,
  FileArchive,
  Loader2,
  RotateCcw,
  Upload,
  XCircle,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { toCsv, triggerDownload, triggerUrlDownload } from '@/lib/csv'
import { errorMessageOf } from '@/lib/handle-server-error'
import { STATUS_LEVEL_CLASS, statusTextClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { personsApi } from '../api/persons-api'
import { useCreateFaceImportJob, useFaceImportJob } from '../hooks/use-persons'
import { usePersonsContext, usePersonsState } from './persons-provider'

/** 状态 → 本地化文案 key。查表而不是链式三元：新增状态时不会漏一个分支。 */
const STATUS_LABEL_KEYS = {
  PENDING: 'faceImport.statusPending',
  EXTRACTING: 'faceImport.statusExtracting',
  PROCESSING: 'faceImport.statusProcessing',
  COMPLETED: 'faceImport.statusCompleted',
  FAILED: 'faceImport.statusFailed',
  CANCELLED: 'faceImport.statusCancelled',
} as const

function downloadManifestTemplate() {
  const csv = toCsv(
    ['name', 'personId', 'imagePath', 'isPrimary'],
    [
      ['张三', 'EMP-001', 'images/EMP001_1.jpg', 'true'],
      ['张三', 'EMP-001', 'images/EMP001_2.jpg', 'false'],
      ['李四', 'EMP-002', 'images/EMP002.png', 'true'],
    ]
  )
  triggerDownload(
    new Blob([csv], { type: 'text/csv;charset=utf-8;' }),
    'manifest_template.csv'
  )
}

export function PersonsFaceImportDialog() {
  const { t } = useTranslation('persons')
  const { open } = usePersonsState()
  const {
    setOpen,
    faceImportJobId: activeJobId,
    setFaceImportJobId,
  } = usePersonsContext()
  const fileInputRef = useRef<HTMLInputElement>(null)

  const createMutation = useCreateFaceImportJob()
  const isOpen = open === 'import-faces'

  // 任务 id 与轮询都挂在 Provider 上：弹窗关闭（组件卸载）期间任务照常推进，
  // 完成后由 Provider 统一刷新列表。这里只读状态。
  const jobQuery = useFaceImportJob(activeJobId, true)
  const job = jobQuery.data ?? null

  const isTerminal =
    job?.status === 'COMPLETED' ||
    job?.status === 'FAILED' ||
    job?.status === 'CANCELLED'
  const isBusy = createMutation.isPending || (Boolean(job) && !isTerminal)
  const isJobQueryError = Boolean(activeJobId) && jobQuery.isError

  const reset = () => {
    setFaceImportJobId(null)
    createMutation.reset()
    if (fileInputRef.current) fileInputRef.current.value = ''
  }

  const handleClose = () => {
    if (isBusy) {
      // 轮询与完成后的失效都在 Provider 上，关闭弹窗不会中断任务跟踪。
      toast.info(t('faceImport.runningInBackground'))
    }
    setOpen(null)
  }

  const selectFile = async (selected?: File) => {
    if (!selected) return
    const isZip =
      selected.name.toLowerCase().endsWith('.zip') ||
      selected.type === 'application/zip' ||
      selected.type === 'application/x-zip-compressed'
    if (!isZip) {
      toast.error(t('faceImport.selectZipError'))
      return
    }

    try {
      const resp = await createMutation.mutateAsync(selected)
      setFaceImportJobId(resp.jobId)
      toast.success(t('faceImport.jobStarted'))
    } catch (err: unknown) {
      toast.error(errorMessageOf(err))
    }
  }

  const downloadFailedReport = () => {
    if (!activeJobId) return
    triggerUrlDownload(
      personsApi.getFaceImportFailedReportUrl(activeJobId),
      `failed_report_${activeJobId}.csv`
    )
  }

  const total = job?.total ?? 0
  const processed = job?.processed ?? 0
  const succeeded = job?.succeeded ?? 0
  const failed = job?.failed ?? 0
  const percent =
    total > 0 ? Math.min(100, Math.round((processed / total) * 100)) : 0

  return (
    <Dialog
      open={isOpen}
      onOpenChange={(nextOpen) => !nextOpen && handleClose()}
    >
      <DialogContent className='max-w-xl'>
        <DialogHeader>
          <DialogTitle>{t('faceImport.title')}</DialogTitle>
          <DialogDescription>{t('faceImport.description')}</DialogDescription>
        </DialogHeader>

        {!activeJobId ? (
          <div className='space-y-4'>
            <input
              ref={fileInputRef}
              type='file'
              accept='.zip,application/zip,application/x-zip-compressed'
              className='hidden'
              onChange={(event) => void selectFile(event.target.files?.[0])}
            />

            <div className='grid gap-3 sm:grid-cols-[1fr_auto] sm:items-stretch'>
              <button
                type='button'
                disabled={createMutation.isPending}
                onClick={() => fileInputRef.current?.click()}
                onDragOver={(event) => event.preventDefault()}
                onDrop={(event) => {
                  event.preventDefault()
                  void selectFile(event.dataTransfer.files?.[0])
                }}
                className='flex flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed border-border p-6 text-center transition-colors hover:border-primary hover:bg-muted/40 disabled:cursor-not-allowed disabled:opacity-60'
              >
                {createMutation.isPending ? (
                  <Loader2 className='size-8 animate-spin text-primary' />
                ) : (
                  <Upload className='size-8 text-muted-foreground' />
                )}
                <span className='font-medium text-foreground'>
                  {t('faceImport.chooseZip')}
                </span>
                <span className='text-xs text-muted-foreground'>
                  {t('faceImport.fileHint')}
                </span>
              </button>

              <Button
                type='button'
                variant='outline'
                className='flex h-auto min-h-16 flex-col items-center justify-center gap-1.5 p-4'
                onClick={downloadManifestTemplate}
              >
                <Download className='size-4' />
                <span>{t('faceImport.downloadTemplate')}</span>
                <span className='text-xs text-muted-foreground'>
                  manifest.csv
                </span>
              </Button>
            </div>

            <div className='space-y-1.5 rounded-lg border border-border bg-muted/30 p-3 text-xs text-muted-foreground'>
              <div className='flex items-center gap-1.5 font-medium text-foreground'>
                <FileArchive className='size-3.5' />
                <span>{t('faceImport.specTitle')}</span>
              </div>
              <ul className='list-inside list-disc space-y-0.5 ps-1'>
                <li>{t('faceImport.spec1')}</li>
                <li>{t('faceImport.spec2')}</li>
                <li>{t('faceImport.spec3')}</li>
              </ul>
            </div>
          </div>
        ) : isJobQueryError ? (
          <div className='flex flex-col items-center justify-center gap-2 py-8 text-center text-destructive'>
            <AlertCircle className='size-6' />
            <span className='font-medium'>{t('table.loadFailed')}</span>
            <Button
              type='button'
              size='sm'
              variant='outline'
              onClick={() => void jobQuery.refetch()}
              className='mt-1'
            >
              {t('actions.retry')}
            </Button>
          </div>
        ) : (
          <div className='space-y-4 py-2'>
            <div className='flex items-center justify-between'>
              <div className='flex items-center gap-2'>
                {isBusy ? (
                  <Loader2 className='size-5 animate-spin text-primary' />
                ) : job?.status === 'COMPLETED' ? (
                  <CheckCircle2
                    className={cn('size-5', statusTextClass('active'))}
                  />
                ) : (
                  <XCircle className='size-5 text-destructive' />
                )}
                <span className='font-medium'>
                  {job ? t(STATUS_LABEL_KEYS[job.status]) : null}
                </span>
              </div>
              <Badge
                variant={job?.status === 'COMPLETED' ? 'default' : 'secondary'}
              >
                {job?.status ?? 'PENDING'}
              </Badge>
            </div>

            <div className='space-y-1.5'>
              <div className='flex justify-between text-xs text-muted-foreground'>
                <span>{t('faceImport.progressLabel')}</span>
                <span>{percent}%</span>
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
                <p className='truncate text-xs text-muted-foreground'>
                  {t('faceImport.currentFile')} {job.currentFile}
                </p>
              )}
            </div>

            <div className='grid grid-cols-4 gap-2 text-center'>
              <div className='rounded-md border border-border bg-muted/20 p-2'>
                <div className='text-xs text-muted-foreground'>
                  {t('faceImport.total')}
                </div>
                <div className='text-lg font-semibold'>{total}</div>
              </div>
              <div className='rounded-md border border-border bg-muted/20 p-2'>
                <div className='text-xs text-muted-foreground'>
                  {t('faceImport.processed')}
                </div>
                <div className='text-lg font-semibold'>{processed}</div>
              </div>
              <div
                className={cn(
                  'rounded-md border p-2',
                  STATUS_LEVEL_CLASS.active
                )}
              >
                <div className='text-xs'>{t('faceImport.succeeded')}</div>
                <div className='text-lg font-semibold'>{succeeded}</div>
              </div>
              <div
                className={cn(
                  'rounded-md border p-2',
                  STATUS_LEVEL_CLASS.destructive
                )}
              >
                <div className='text-xs'>{t('faceImport.failed')}</div>
                <div className='text-lg font-semibold'>{failed}</div>
              </div>
            </div>

            {job?.status === 'FAILED' && (
              <Alert variant='destructive'>
                <AlertCircle className='size-4' />
                <AlertTitle>{t('faceImport.errorTitle')}</AlertTitle>
                <AlertDescription>
                  {job.errorMessage || t('faceImport.unknownError')}
                </AlertDescription>
              </Alert>
            )}

            {job?.status === 'COMPLETED' && (
              <Alert className={cn('border', STATUS_LEVEL_CLASS.active)}>
                <CheckCircle2 className='size-4' />
                <AlertTitle>{t('faceImport.completeTitle')}</AlertTitle>
                <AlertDescription>
                  {t('faceImport.completeSummary', { succeeded, failed })}
                </AlertDescription>
              </Alert>
            )}
          </div>
        )}

        <DialogFooter className='gap-2 sm:justify-between'>
          {activeJobId ? (
            <>
              {isTerminal && failed > 0 ? (
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  onClick={downloadFailedReport}
                  className='border-destructive/30 text-destructive hover:bg-destructive/10'
                >
                  <Download className='me-1.5 size-4' />
                  <span>{t('faceImport.downloadFailedReport')}</span>
                </Button>
              ) : (
                <div />
              )}

              <div className='flex gap-2'>
                {isTerminal ? (
                  <>
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      onClick={reset}
                    >
                      <RotateCcw className='me-1.5 size-4' />
                      <span>{t('faceImport.retryAnother')}</span>
                    </Button>
                    <Button type='button' size='sm' onClick={handleClose}>
                      {t('actions.confirm')}
                    </Button>
                  </>
                ) : (
                  <Button
                    type='button'
                    variant='secondary'
                    size='sm'
                    onClick={handleClose}
                  >
                    {t('faceImport.runInBackground')}
                  </Button>
                )}
              </div>
            </>
          ) : (
            <div className='flex w-full justify-end'>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                onClick={() => setOpen(null)}
              >
                {t('actions.cancel')}
              </Button>
            </div>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
