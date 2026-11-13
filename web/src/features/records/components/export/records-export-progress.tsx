import { Loader2, RotateCcw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { type ExportTaskDraft } from '../../lib/export-db'
import { type ExportProgress } from '../../lib/export-zip-pipeline'

export function ExportDraftBanner({
  draft,
  onDiscard,
  onResume,
}: {
  draft: ExportTaskDraft
  onDiscard: () => void
  onResume: () => void
}) {
  const { t } = useTranslation('records')

  return (
    <div className='flex animate-in items-center justify-between gap-3 rounded-lg border border-primary/30 bg-primary/5 p-3 text-xs duration-200 fade-in'>
      <div className='flex items-center gap-2'>
        <RotateCcw className='size-4 shrink-0 animate-pulse text-primary' />
        <div>
          <div className='font-semibold text-foreground'>
            {t('export.draftFoundTitle')}
          </div>
          <div className='text-[11px] text-muted-foreground'>
            {t('export.draftFoundDesc', {
              current: draft.completedKeys.length,
              total: draft.totalCount,
            })}
          </div>
          <div className='text-[11px] text-muted-foreground'>
            {t('export.resumeHint')}
          </div>
        </div>
      </div>

      <div className='flex items-center gap-1.5'>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          className='h-7 px-2 text-xs text-muted-foreground hover:text-foreground'
          onClick={onDiscard}
        >
          {t('export.discardDraft')}
        </Button>
        <Button
          type='button'
          size='sm'
          className='h-7 gap-1 px-2.5 text-xs'
          onClick={onResume}
        >
          <RotateCcw className='size-3' />
          <span>{t('export.resumeDraft')}</span>
        </Button>
      </div>
    </div>
  )
}

export function ExportProgressPanel({
  progress,
}: {
  progress: ExportProgress | null
}) {
  const { t } = useTranslation('records')

  const percent = progress
    ? (progress.percent ??
      (progress.total > 0
        ? Math.min(100, Math.round((progress.current / progress.total) * 100))
        : 0))
    : 0

  return (
    <div className='my-4 space-y-4 rounded-lg border border-border bg-muted/20 p-4'>
      <div
        role='status'
        aria-live='polite'
        className='flex items-center justify-between text-xs font-medium'
      >
        <span className='flex items-center gap-2'>
          <Loader2 className='size-3.5 animate-spin text-primary' />
          <span>{progress?.message || t('export.inProgress')}</span>
        </span>
        <span className='font-mono'>{percent}%</span>
      </div>

      <div
        role='progressbar'
        aria-label={t('export.progressLabel')}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={percent}
        className='h-2 w-full overflow-hidden rounded-full bg-secondary'
      >
        <div
          className='h-full bg-primary transition-all duration-300'
          style={{ width: `${percent}%` }}
        />
      </div>

      <p className='text-center text-[11px] text-muted-foreground'>
        {t('export.offloadHint')}
      </p>
    </div>
  )
}
