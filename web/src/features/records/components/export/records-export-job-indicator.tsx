import { useEffect, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { Loader2, RotateCcw, TriangleAlert, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { useExportJobStore } from '../../lib/export-job-store'

/**
 * 挂在全局布局上的导出状态卡（跨路由可见）。
 *
 * 文案刻意不提「后台」：浏览器没有真正的后台任务，页面被销毁任务就没了。
 * 能兑现的承诺是**中断后可续**（已完成的图片已写进 IndexedDB），所以卡片
 * 说的是「请保持此标签页打开」+「已保存 N 张」。
 *
 * 另有一个「上次导出未完成」入口：草稿只在导出对话框里被发现的话，用户
 * 导航走就再也不会知道有东西没做完，这是真正会丢活的地方。
 */
export function RecordsExportJobIndicator() {
  const { t } = useTranslation('records')
  const navigate = useNavigate()
  const status = useExportJobStore((s) => s.status)
  const progress = useExportJobStore((s) => s.progress)
  const error = useExportJobStore((s) => s.error)
  const cancel = useExportJobStore((s) => s.cancel)
  const dismiss = useExportJobStore((s) => s.dismiss)

  const [pending, setPending] = useState<{
    taskId: string
    saved: number
    total: number
  } | null>(null)

  const isRunning = status === 'running'
  const isFailed = status === 'failed'

  // 空闲时探一次未完成任务：动态 import 让布局不必为 IndexedDB 付首屏代价
  useEffect(() => {
    if (isRunning) return

    let cancelled = false
    void (async () => {
      const { exportDb } = await import('../../lib/export-db')
      const draft = await exportDb.getLatestPendingTask().catch(() => null)
      if (cancelled || !draft || draft.status !== 'in_progress') return
      // 一张都没存下来的任务没什么可救的，别拿它打扰用户
      if (draft.completedKeys.length === 0) return
      setPending({
        taskId: draft.taskId,
        saved: draft.completedKeys.length,
        total: draft.totalCount,
      })
    })()

    return () => {
      cancelled = true
    }
  }, [isRunning, status])

  // 任务在跑时不让旧草稿抢占版面，但也不必为此在 effect 里写状态
  const pendingDraft = isRunning ? null : pending

  const handleDiscardPending = () => {
    const taskId = pendingDraft?.taskId
    setPending(null)
    if (!taskId) return
    void (async () => {
      const { exportDb } = await import('../../lib/export-db')
      await exportDb.clearTaskDraft(taskId).catch(() => undefined)
    })()
  }

  const handleResumePending = () => {
    useExportJobStore.getState().requestResume()
    // 回记录页并打开对话框。搜索参数用默认值即可：续传吃的是草稿里保存的
    // queryConfig 快照，与当前页面的筛选条件无关（同 use-person-deep-links）。
    void navigate({
      to: '/records',
      search: { tab: 'recognitions', view: 'table', page: 1, pageSize: 20 },
    })
  }

  if (!isRunning && !isFailed) {
    if (!pendingDraft) return null

    return (
      <div
        role='status'
        aria-live='polite'
        className='fixed end-4 bottom-24 z-50 w-[min(22rem,calc(100vw-2rem))] animate-in rounded-xl border border-primary/40 bg-background/95 p-3 shadow-xl backdrop-blur-md duration-200 fade-in slide-in-from-bottom-4'
      >
        <div className='flex items-start gap-2'>
          <RotateCcw className='mt-0.5 size-4 shrink-0 text-primary' />
          <div className='min-w-0 flex-1 space-y-1'>
            <div className='text-xs font-semibold'>
              {t('export.pendingBannerTitle')}
            </div>
            <p className='text-[11px] leading-snug text-muted-foreground'>
              {t('export.pendingBannerDesc', {
                current: pendingDraft.saved,
                total: pendingDraft.total,
              })}
            </p>
            <div className='flex items-center gap-1.5 pt-0.5'>
              <Button
                type='button'
                size='sm'
                className='h-6 px-2 text-[11px]'
                onClick={handleResumePending}
              >
                {t('export.pendingBannerResume')}
              </Button>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                className='h-6 px-2 text-[11px] text-muted-foreground hover:text-foreground'
                onClick={handleDiscardPending}
              >
                {t('export.pendingBannerDiscard')}
              </Button>
            </div>
          </div>
        </div>
      </div>
    )
  }

  const percent = progress
    ? (progress.percent ??
      (progress.total > 0
        ? Math.min(100, Math.round((progress.current / progress.total) * 100))
        : 0))
    : 0

  const saved = isRunning ? progress?.saved : undefined
  const title = isRunning ? t('export.jobRunning') : t('export.jobFailedTitle')
  const detail = isRunning
    ? (progress?.message ?? t('export.inProgress'))
    : (error ?? t('export.failed'))

  return (
    <div
      role='status'
      aria-live='polite'
      className='fixed end-4 bottom-24 z-50 w-[min(20rem,calc(100vw-2rem))] animate-in rounded-xl border border-border/80 bg-background/95 p-3 shadow-xl backdrop-blur-md duration-200 fade-in slide-in-from-bottom-4'
    >
      <div className='flex items-start gap-2'>
        <span className='mt-0.5 shrink-0'>
          {isRunning ? (
            <Loader2 className='size-4 animate-spin text-primary' />
          ) : (
            <TriangleAlert className='size-4 text-destructive' />
          )}
        </span>

        <div className='min-w-0 flex-1 space-y-1.5'>
          <div className='flex items-center justify-between gap-2 text-xs font-semibold'>
            <span className='truncate'>{title}</span>
            {isRunning && (
              <span className='font-mono text-[11px] text-muted-foreground'>
                {percent}%
              </span>
            )}
          </div>

          <p className='line-clamp-2 text-[11px] leading-snug text-muted-foreground'>
            {detail}
          </p>

          {isRunning && (
            <div
              role='progressbar'
              aria-label={t('export.progressLabel')}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={percent}
              className='h-1.5 w-full overflow-hidden rounded-full bg-secondary'
            >
              <div
                className='h-full bg-primary transition-all duration-300'
                style={{ width: `${percent}%` }}
              />
            </div>
          )}

          {isRunning && saved !== undefined && saved > 0 && (
            <p className='text-[11px] leading-snug text-primary/90'>
              {t('export.savedSoFar', {
                saved,
                total: progress?.total ?? 0,
              })}
            </p>
          )}

          {isRunning && (
            <p className='text-[11px] leading-snug text-muted-foreground/80'>
              {t('export.keepTabOpen')}
            </p>
          )}
        </div>

        <div className='flex shrink-0 items-center gap-1'>
          {isRunning ? (
            <Button
              type='button'
              variant='ghost'
              size='sm'
              className='h-6 px-2 text-[11px] text-muted-foreground hover:text-foreground'
              onClick={cancel}
            >
              {t('export.cancelJob')}
            </Button>
          ) : (
            <Button
              type='button'
              variant='ghost'
              size='icon'
              className='size-6'
              aria-label={t('export.dismissJob')}
              onClick={dismiss}
            >
              <X className='size-3.5' />
            </Button>
          )}
        </div>
      </div>
    </div>
  )
}
