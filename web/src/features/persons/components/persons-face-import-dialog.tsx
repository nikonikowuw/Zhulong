import { useRef } from 'react'
import { Download, FileArchive, Loader2, RotateCcw, Upload } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { toCsv, triggerDownload, triggerUrlDownload } from '@/lib/csv'
import { errorMessageOf } from '@/lib/handle-server-error'
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
import { FaceJobProgressPanel } from './face-job-progress-panel'
import { usePersonsContext } from './persons-provider'

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
  const {
    open,
    setOpen,
    faceImportJobId: activeJobId,
    setFaceImportJobId,
  } = usePersonsContext()
  const fileInputRef = useRef<HTMLInputElement>(null)

  const createMutation = useCreateFaceImportJob()
  const isOpen = open === 'import-faces'

  // 轮询间隔由 Provider 统一驱动；这里仅订阅共享 query 缓存，避免双份定时器。
  const jobQuery = useFaceImportJob(activeJobId, true, { poll: false })
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

  const succeeded = job?.succeeded ?? 0
  const failed = job?.failed ?? 0

  return (
    <Dialog
      open={isOpen}
      onOpenChange={(nextOpen) => !nextOpen && handleClose()}
    >
      <DialogContent className='overflow-hidden sm:max-w-xl'>
        <DialogHeader className='min-w-0'>
          <DialogTitle>{t('faceImport.title')}</DialogTitle>
          <DialogDescription>{t('faceImport.description')}</DialogDescription>
        </DialogHeader>

        {!activeJobId ? (
          <div className='min-w-0 space-y-4'>
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
        ) : (
          <FaceJobProgressPanel
            job={job}
            isBusy={isBusy}
            isQueryError={isJobQueryError}
            onRetryQuery={() => void jobQuery.refetch()}
            statusLabel={job ? t(STATUS_LABEL_KEYS[job.status]) : ''}
            statusBadgeLabel={job?.status ?? 'PENDING'}
            progressLabel={t('faceImport.progressLabel')}
            currentFileLabel={t('faceImport.currentFile')}
            statLabels={{
              total: t('faceImport.total'),
              processed: t('faceImport.processed'),
              succeeded: t('faceImport.succeeded'),
              failed: t('faceImport.failed'),
            }}
            alerts={{
              errorTitle: t('faceImport.errorTitle'),
              unknownError: t('faceImport.unknownError'),
              completeTitle: t('faceImport.completeTitle'),
              completeSummary: t('faceImport.completeSummary', {
                succeeded,
                failed,
              }),
            }}
          />
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
