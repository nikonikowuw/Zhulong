import { memo, useMemo } from 'react'
import { AxiosError } from 'axios'
import {
  AlertCircle,
  Cpu,
  Inbox,
  Info,
  LayoutGrid,
  List,
  Network,
  RotateCw,
  ScanFace,
  Sparkles,
  UploadCloud,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { errorMessageOf } from '@/lib/handle-server-error'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import {
  ViewModeSwitch,
  type ViewModeOption,
} from '@/components/view-mode-switch'
import { LOWER_RETRY_THRESHOLD } from '../data/match-utils'
import { type ImageSearchMatchInfo } from '../data/schema'
import { ImageSearchMatchCard } from './image-search-match-card'
import { ImageSearchMatchRow } from './image-search-match-row'

interface ImageSearchResultsProps {
  results: ImageSearchMatchInfo[] | null
  isSearching: boolean
  isError: boolean
  error: Error | null
  appliedThreshold?: number
  appliedTopK: number
  viewMode?: 'grid' | 'list'
  onViewModeChange?: (mode: 'grid' | 'list') => void
  onInspect: (personId: string) => void
  onRetry?: () => void
  onLowerThresholdAndSearch?: () => void
}

const GENERIC_FALLBACK_MSG = 'Something went wrong!'

function resolveSearchErrorMessage(
  error: Error | null,
  fallback: string
): string {
  if (!error) return fallback
  if (!(error instanceof AxiosError) && error.message.trim().length === 0) {
    return fallback
  }
  const resolved = errorMessageOf(error)
  if (!resolved || resolved === GENERIC_FALLBACK_MSG) {
    return fallback
  }
  return resolved
}

export const ImageSearchResults = memo(function ImageSearchResults({
  results,
  isSearching,
  isError,
  error,
  appliedThreshold,
  appliedTopK,
  viewMode = 'grid',
  onViewModeChange,
  onInspect,
  onRetry,
  onLowerThresholdAndSearch,
}: ImageSearchResultsProps) {
  const { t } = useTranslation('imageSearch')

  const viewModeOptions = useMemo<
    ReadonlyArray<ViewModeOption<'grid' | 'list'>>
  >(
    () => [
      { value: 'grid', label: t('controls.viewGrid'), icon: LayoutGrid },
      { value: 'list', label: t('controls.viewList'), icon: List },
    ],
    [t]
  )

  // 加载检索中骨架屏状态
  if (isSearching) {
    return (
      <div className='flex h-full flex-1 flex-col gap-4'>
        <div className='flex items-center justify-between rounded-xl border border-primary/20 bg-primary/5 px-4 py-2.5 backdrop-blur-xs'>
          <div className='flex items-center gap-2'>
            <div className='h-2 w-2 animate-ping rounded-full bg-primary' />
            <span className='text-xs font-semibold text-primary'>
              {t('controls.searching')}
            </span>
          </div>
          <Skeleton className='h-4 w-28' />
        </div>

        <div className='grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4'>
          {Array.from({ length: Math.min(appliedTopK, 6) }).map((_, i) => (
            <div
              key={i}
              className='flex flex-col overflow-hidden rounded-2xl border border-border/70 bg-card/60 p-3.5 shadow-xs'
            >
              <Skeleton className='aspect-square w-full rounded-xl' />
              <div className='mt-3.5 space-y-2.5'>
                <div className='flex items-center justify-between'>
                  <Skeleton className='h-4 w-1/2' />
                  <Skeleton className='h-4 w-14' />
                </div>
                <Skeleton className='h-2 w-full rounded-full' />
                <div className='mt-2 flex gap-1.5 pt-1'>
                  <Skeleton className='h-7 flex-1 rounded-md' />
                  <Skeleton className='h-7 flex-1 rounded-md' />
                  <Skeleton className='h-7 flex-1 rounded-md' />
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    )
  }

  // 检索失败错误状态（含重试入口）
  if (isError) {
    const errorText = resolveSearchErrorMessage(
      error,
      t('results.errorDescription')
    )
    return (
      <Card className='relative flex h-full min-h-[420px] flex-1 flex-col items-center justify-center overflow-hidden rounded-2xl border border-destructive/30 bg-destructive/5 p-8 text-center shadow-xs backdrop-blur-xs lg:min-h-full'>
        <div className='pointer-events-none absolute inset-0 bg-[radial-gradient(ellipse_at_center,color-mix(in_oklch,var(--destructive)_8%,transparent),transparent_70%)]' />
        <div className='relative mb-3.5 flex h-14 w-14 items-center justify-center rounded-2xl border border-destructive/30 bg-destructive/10 text-destructive shadow-xs'>
          <AlertCircle className='h-7 w-7' />
        </div>
        <h3 className='relative text-base font-bold text-destructive'>
          {t('results.errorTitle')}
        </h3>
        <p className='relative mt-1.5 max-w-md text-xs leading-relaxed text-muted-foreground'>
          {errorText}
        </p>
        {onRetry && (
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='relative mt-4 gap-1.5 text-xs'
            onClick={onRetry}
          >
            <RotateCw className='h-3.5 w-3.5' />
            <span>{t('results.retry')}</span>
          </Button>
        )}
      </Card>
    )
  }

  // 就绪空闲状态 (未上传或上传后尚未发起检索)
  if (results === null) {
    return (
      <Card className='relative flex h-full min-h-[440px] flex-1 flex-col items-center justify-center overflow-hidden rounded-2xl border border-border/70 bg-card/60 p-6 text-center shadow-xs backdrop-blur-xs lg:min-h-full'>
        <div className='pointer-events-none absolute inset-0 bg-[radial-gradient(ellipse_at_center,color-mix(in_oklch,var(--primary)_6%,transparent),transparent_70%)]' />
        <div className='pointer-events-none absolute inset-0 [background-image:linear-gradient(to_right,currentColor_1px,transparent_1px),linear-gradient(to_bottom,currentColor_1px,transparent_1px)] [background-size:24px_24px] opacity-[0.02] dark:opacity-[0.05]' />

        <div className='relative mb-4 flex h-16 w-16 items-center justify-center rounded-2xl border border-primary/20 bg-primary/10 text-primary shadow-xs'>
          <ScanFace className='h-8 w-8 transition-transform duration-300 hover:scale-110' />
          <div className='absolute -end-1 -top-1 flex h-4.5 w-4.5 items-center justify-center rounded-full bg-primary text-[10px] text-primary-foreground shadow-xs'>
            <Sparkles className='h-2.5 w-2.5' />
          </div>
        </div>

        <h3 className='relative text-base font-bold tracking-tight text-foreground'>
          {t('results.idleTitle')}
        </h3>
        <p className='relative mt-1.5 max-w-md text-xs leading-relaxed text-muted-foreground'>
          {t('results.idleDescription')}
        </p>

        {/* 3 步工作流引导卡片 */}
        <div className='relative mt-6 grid w-full max-w-2xl grid-cols-1 gap-3 sm:grid-cols-3'>
          <div className='flex flex-col items-center rounded-xl border border-border/60 bg-muted/20 p-3.5 text-center transition-all hover:border-primary/30 hover:bg-muted/40'>
            <div className='mb-2 flex h-8 w-8 items-center justify-center rounded-lg bg-primary/10 text-primary'>
              <UploadCloud className='h-4 w-4' />
            </div>
            <span className='text-xs font-semibold text-foreground'>
              {t('results.workflowStep1')}
            </span>
            <span className='mt-1 text-[11px] leading-snug text-muted-foreground'>
              {t('results.workflowStep1Desc')}
            </span>
          </div>

          <div className='flex flex-col items-center rounded-xl border border-border/60 bg-muted/20 p-3.5 text-center transition-all hover:border-primary/30 hover:bg-muted/40'>
            <div className='mb-2 flex h-8 w-8 items-center justify-center rounded-lg bg-primary/10 text-primary'>
              <Cpu className='h-4 w-4' />
            </div>
            <span className='text-xs font-semibold text-foreground'>
              {t('results.workflowStep2')}
            </span>
            <span className='mt-1 text-[11px] leading-snug text-muted-foreground'>
              {t('results.workflowStep2Desc')}
            </span>
          </div>

          <div className='flex flex-col items-center rounded-xl border border-border/60 bg-muted/20 p-3.5 text-center transition-all hover:border-primary/30 hover:bg-muted/40'>
            <div className='mb-2 flex h-8 w-8 items-center justify-center rounded-lg bg-primary/10 text-primary'>
              <Network className='h-4 w-4' />
            </div>
            <span className='text-xs font-semibold text-foreground'>
              {t('results.workflowStep3')}
            </span>
            <span className='mt-1 text-[11px] leading-snug text-muted-foreground'>
              {t('results.workflowStep3Desc')}
            </span>
          </div>
        </div>

        {/* 专业研判指南贴士卡片 */}
        <div className='relative mt-5 w-full max-w-2xl rounded-xl border border-border/60 bg-muted/30 p-3.5 text-start text-xs'>
          <div className='flex items-center gap-1.5 font-semibold text-foreground'>
            <Sparkles className='h-3.5 w-3.5 text-primary' />
            <span>{t('results.tipsTitle')}</span>
          </div>
          <ul className='mt-2 space-y-1.5 text-[11px] text-muted-foreground'>
            <li className='flex items-start gap-1.5'>
              <span className='mt-1 h-1 w-1 shrink-0 rounded-full bg-primary' />
              <span>{t('results.tipsItem1')}</span>
            </li>
            <li className='flex items-start gap-1.5'>
              <span className='mt-1 h-1 w-1 shrink-0 rounded-full bg-primary' />
              <span>{t('results.tipsItem2')}</span>
            </li>
            <li className='flex items-start gap-1.5'>
              <span className='mt-1 h-1 w-1 shrink-0 rounded-full bg-primary' />
              <span>{t('results.tipsItem3')}</span>
            </li>
          </ul>
        </div>
      </Card>
    )
  }

  // 检索完成但无匹配人员
  if (results.length === 0) {
    const canLowerThreshold =
      onLowerThresholdAndSearch &&
      (appliedThreshold === undefined ||
        appliedThreshold > LOWER_RETRY_THRESHOLD)

    return (
      <Card className='relative flex h-full min-h-[420px] flex-1 flex-col items-center justify-center overflow-hidden rounded-2xl border border-border/70 bg-card/60 p-8 text-center shadow-xs backdrop-blur-xs lg:min-h-full'>
        <div className='mb-3.5 flex h-14 w-14 items-center justify-center rounded-2xl border border-muted bg-muted/50 text-muted-foreground shadow-xs'>
          <Inbox className='h-7 w-7' />
        </div>
        <h3 className='text-base font-bold tracking-tight text-foreground'>
          {t('results.emptyTitle')}
        </h3>
        <p className='mt-1.5 max-w-md text-xs leading-relaxed text-muted-foreground'>
          {t('results.emptyDescription')}
        </p>

        {canLowerThreshold && (
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='mt-4 gap-1.5 border-primary/30 bg-primary/5 text-xs text-primary hover:bg-primary/10'
            onClick={onLowerThresholdAndSearch}
          >
            <Sparkles className='h-3.5 w-3.5' />
            <span>{t('results.emptyActionLowerThreshold')}</span>
          </Button>
        )}
      </Card>
    )
  }

  return (
    <div className='flex h-full flex-1 flex-col gap-3'>
      {/* 结果栏 Header Toolbar（基于实际生效检索快照展示条件） */}
      <div className='flex flex-wrap items-center justify-between gap-3 rounded-xl border border-border/70 bg-card/50 px-4 py-2.5 shadow-2xs backdrop-blur-xs'>
        <div className='flex flex-wrap items-center gap-2'>
          <span className='text-xs font-bold text-foreground'>
            {t('results.title')}
          </span>
          <Badge
            variant='secondary'
            className='border border-primary/20 bg-primary/10 px-2 py-0.5 font-mono text-[11px] font-semibold text-primary'
          >
            {t('results.count', { count: results.length })}
          </Badge>
          {appliedThreshold !== undefined && (
            <span className='hidden font-mono text-[11px] text-muted-foreground sm:inline-block'>
              {t('results.filterCriteria', {
                threshold: Math.round(appliedThreshold * 100),
                topK: appliedTopK,
              })}
            </span>
          )}
        </div>

        {onViewModeChange && (
          <ViewModeSwitch
            value={viewMode}
            options={viewModeOptions}
            onChange={onViewModeChange}
          />
        )}
      </div>

      {/* 人工核验合规提示 */}
      <div className='flex items-center gap-2 rounded-lg border border-border/60 bg-muted/30 px-3 py-2 text-xs text-muted-foreground'>
        <Info className='h-3.5 w-3.5 shrink-0 text-primary' />
        <span>{t('disclaimer')}</span>
      </div>

      {/* 结果主体流式展示：相册网格 / 紧凑列表 */}
      {viewMode === 'grid' ? (
        <div className='grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4'>
          {results.map((match, index) => (
            <ImageSearchMatchCard
              key={match.personId}
              match={match}
              rank={index + 1}
              onInspect={onInspect}
            />
          ))}
        </div>
      ) : (
        <div className='flex flex-col gap-2.5'>
          {results.map((match, index) => (
            <ImageSearchMatchRow
              key={match.personId}
              match={match}
              rank={index + 1}
              onInspect={onInspect}
            />
          ))}
        </div>
      )}
    </div>
  )
})
