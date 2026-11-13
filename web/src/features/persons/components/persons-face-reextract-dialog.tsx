import { Download, Loader2, RotateCcw, ScanFace } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { triggerUrlDownload } from '@/lib/csv'
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
import {
  useCancelFaceReextractJob,
  useCreateFaceReextractJob,
  useFaceReextractJob,
} from '../hooks/use-persons'
import { FaceJobProgressPanel } from './face-job-progress-panel'
import { usePersonsContext, usePersonsState } from './persons-provider'

const STATUS_LABEL_KEYS = {
  PENDING: 'faceReextract.statusPending',
  EXTRACTING: 'faceReextract.statusProcessing',
  PROCESSING: 'faceReextract.statusProcessing',
  COMPLETED: 'faceReextract.statusCompleted',
  FAILED: 'faceReextract.statusFailed',
  CANCELLED: 'faceReextract.statusCancelled',
} as const

const STATUS_BADGE_KEYS = {
  PENDING: 'faceReextract.statusBadgePending',
  EXTRACTING: 'faceReextract.statusBadgeProcessing',
  PROCESSING: 'faceReextract.statusBadgeProcessing',
  COMPLETED: 'faceReextract.statusBadgeCompleted',
  FAILED: 'faceReextract.statusBadgeFailed',
  CANCELLED: 'faceReextract.statusBadgeCancelled',
} as const

export function PersonsFaceReextractDialog() {
  const { t } = useTranslation('persons')
  const { open, reextractTarget } = usePersonsState()
  const {
    setOpen,
    faceReextractJobId: activeJobId,
    setFaceReextractJobId,
    setReextractTarget,
  } = usePersonsContext()

  const createMutation = useCreateFaceReextractJob()
  const cancelMutation = useCancelFaceReextractJob()
  const isOpen = open === 'reextract-faces'

  const jobQuery = useFaceReextractJob(activeJobId, true)
  const job = jobQuery.data ?? null

  const isTerminal =
    job?.status === 'COMPLETED' ||
    job?.status === 'FAILED' ||
    job?.status === 'CANCELLED'
  const isBusy = createMutation.isPending || (Boolean(job) && !isTerminal)
  const isJobQueryError = Boolean(activeJobId) && jobQuery.isError

  const resetToConfirm = () => {
    setFaceReextractJobId(null)
    createMutation.reset()
  }

  const handleClose = () => {
    if (isBusy) {
      toast.info(t('faceReextract.runningInBackground'))
    } else if (isTerminal) {
      setFaceReextractJobId(null)
      setReextractTarget(null)
    }
    setOpen(null)
  }

  const handleStart = async () => {
    try {
      const resp = await createMutation.mutateAsync(reextractTarget?.personIds)
      setFaceReextractJobId(resp.jobId)
      toast.success(t('faceReextract.jobStarted'))
    } catch (err: unknown) {
      toast.error(errorMessageOf(err))
    }
  }

  const handleCancelJob = async () => {
    if (!activeJobId) return
    try {
      await cancelMutation.mutateAsync(activeJobId)
      toast.info(t('faceReextract.cancelRequested'))
    } catch (err: unknown) {
      toast.error(errorMessageOf(err))
    }
  }

  const downloadFailedReport = () => {
    if (!activeJobId) return
    triggerUrlDownload(
      personsApi.getFaceReextractFailedReportUrl(activeJobId),
      `reextract_failed_report_${activeJobId}.csv`
    )
  }

  const succeeded = job?.succeeded ?? 0
  const failed = job?.failed ?? 0
  const selectedCount = reextractTarget?.personIds.length ?? 0
  const scopeSummary =
    selectedCount === 1 && reextractTarget?.label
      ? t('faceReextract.scopeSingle', { name: reextractTarget.label })
      : selectedCount > 0
        ? t('faceReextract.scopeSelected', { count: selectedCount })
        : t('faceReextract.scopeAll')

  return (
    <Dialog
      open={isOpen}
      onOpenChange={(nextOpen) => !nextOpen && handleClose()}
    >
      <DialogContent className='overflow-hidden sm:max-w-lg'>
        <DialogHeader className='min-w-0'>
          <DialogTitle className='flex items-center gap-2'>
            <ScanFace className='size-5 shrink-0 text-primary' />
            <span>{t('faceReextract.title')}</span>
          </DialogTitle>
          <DialogDescription>
            {t('faceReextract.description')}
          </DialogDescription>
        </DialogHeader>

        {!activeJobId ? (
          <div className='min-w-0 space-y-4 py-1'>
            <div className='rounded-lg border border-border bg-muted/30 p-3 text-sm'>
              <div className='font-medium text-foreground'>
                {t('faceReextract.scopeLabel')}
              </div>
              <p className='mt-1 break-words text-muted-foreground'>
                {scopeSummary}
              </p>
            </div>

            <div className='space-y-1.5 rounded-lg border border-border bg-muted/20 p-3 text-xs text-muted-foreground'>
              <div className='font-medium text-foreground'>
                {t('faceReextract.notesTitle')}
              </div>
              <ul className='list-inside list-disc space-y-1 ps-1'>
                <li>{t('faceReextract.notePreserve')}</li>
                <li>{t('faceReextract.noteFallback')}</li>
                <li>{t('faceReextract.noteResource')}</li>
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
            statusBadgeLabel={t(STATUS_BADGE_KEYS[job?.status ?? 'PENDING'])}
            extraBadge={
              job?.scope
                ? job.scope === 'selected'
                  ? t('faceReextract.badgeSelected')
                  : t('faceReextract.badgeAll')
                : undefined
            }
            progressLabel={t('faceReextract.progressLabel')}
            currentFileLabel={t('faceReextract.currentTarget')}
            statLabels={{
              total: t('faceReextract.total'),
              processed: t('faceReextract.processed'),
              succeeded: t('faceReextract.succeeded'),
              failed: t('faceReextract.failed'),
            }}
            alerts={{
              errorTitle: t('faceReextract.errorTitle'),
              unknownError: t('faceReextract.unknownError'),
              completeTitle: t('faceReextract.completeTitle'),
              completeSummary: t('faceReextract.completeSummary', {
                succeeded,
                failed,
              }),
              cancelledTitle: t('faceReextract.statusCancelled'),
              cancelledSummary: t('faceReextract.cancelledSummary', {
                succeeded,
                failed,
              }),
            }}
          />
        )}

        <DialogFooter className='min-w-0 gap-2 sm:justify-between'>
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
                  <span>{t('faceReextract.downloadFailedReport')}</span>
                </Button>
              ) : (
                <div />
              )}

              <div className='flex flex-wrap justify-end gap-2'>
                {isTerminal ? (
                  <>
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      onClick={resetToConfirm}
                    >
                      <RotateCcw className='me-1.5 size-4' />
                      <span>{t('faceReextract.retryAnother')}</span>
                    </Button>
                    <Button type='button' size='sm' onClick={handleClose}>
                      {t('actions.confirm')}
                    </Button>
                  </>
                ) : (
                  <>
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      disabled={cancelMutation.isPending}
                      onClick={() => void handleCancelJob()}
                      className='border-destructive/30 text-destructive hover:bg-destructive/10'
                    >
                      {cancelMutation.isPending && (
                        <Loader2 className='me-1.5 size-4 animate-spin' />
                      )}
                      <span>{t('faceReextract.cancelJob')}</span>
                    </Button>
                    <Button
                      type='button'
                      variant='secondary'
                      size='sm'
                      onClick={handleClose}
                    >
                      {t('faceReextract.runInBackground')}
                    </Button>
                  </>
                )}
              </div>
            </>
          ) : (
            <div className='flex w-full justify-end gap-2'>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                onClick={() => setOpen(null)}
              >
                {t('actions.cancel')}
              </Button>
              <Button
                type='button'
                size='sm'
                disabled={createMutation.isPending}
                onClick={() => void handleStart()}
              >
                {createMutation.isPending && (
                  <Loader2 className='me-1.5 size-4 animate-spin' />
                )}
                <span>{t('faceReextract.startAction')}</span>
              </Button>
            </div>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
