import {
  AlertCircle,
  Cpu,
  Inbox,
  Layers,
  Network,
  ScanFace,
  Sparkles,
  UploadCloud,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { type ImageSearchMatchInfo } from '../data/schema'
import { ImageSearchMatchRow } from './image-search-match-row'

interface ImageSearchResultsProps {
  results: ImageSearchMatchInfo[] | null
  isSearching: boolean
  isError: boolean
  error: Error | null
  topK: number
}

function getErrorMessage(error: Error | null, fallback: string): string {
  if (!error) return fallback
  const maybeAxios = error as { response?: { data?: { message?: string } } }
  if (typeof maybeAxios.response?.data?.message === 'string') {
    return maybeAxios.response.data.message
  }
  return error.message || fallback
}

export function ImageSearchResults({
  results,
  isSearching,
  isError,
  error,
  topK,
}: ImageSearchResultsProps) {
  const { t } = useTranslation('imageSearch')

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

        <div className='flex h-full min-h-[420px] flex-1 flex-col rounded-2xl border border-border/70 bg-card/40 p-4 shadow-xs backdrop-blur-xs lg:min-h-full'>
          <div className='flex flex-col gap-2.5'>
            {Array.from({ length: Math.min(topK, 6) }).map((_, i) => (
              <div
                key={i}
                className='flex flex-col gap-3 rounded-xl border border-border/70 bg-card/60 p-3 shadow-2xs md:flex-row md:items-center md:justify-between md:gap-4'
              >
                <div className='flex items-center gap-3'>
                  <Skeleton className='h-6 w-9 rounded-md' />
                  <Skeleton className='h-13 w-13 shrink-0 rounded-xl' />
                  <div className='flex flex-col gap-1.5'>
                    <Skeleton className='h-4 w-24' />
                    <Skeleton className='h-3.5 w-16' />
                  </div>
                </div>
                <div className='flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between md:justify-end md:gap-5'>
                  <div className='flex min-w-[170px] flex-col gap-1.5 md:min-w-[200px]'>
                    <div className='flex items-center justify-between'>
                      <Skeleton className='h-3 w-12' />
                      <Skeleton className='h-3.5 w-12' />
                    </div>
                    <Skeleton className='h-1.5 w-full rounded-full' />
                  </div>
                  <div className='flex items-center gap-1.5'>
                    <Skeleton className='h-8 w-20 rounded-md' />
                    <Skeleton className='h-8 w-20 rounded-md' />
                    <Skeleton className='h-8 w-20 rounded-md' />
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>
    )
  }

  // 检索失败错误状态
  if (isError) {
    const errorText = getErrorMessage(error, t('results.errorDescription'))
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
      </Card>
    )
  }

  // 就绪空闲状态 (未上传或上传后尚未发起检索)
  if (results === null) {
    return (
      <Card className='relative flex h-full min-h-[420px] flex-1 flex-col items-center justify-center overflow-hidden rounded-2xl border border-border/70 bg-card/60 p-8 text-center shadow-xs backdrop-blur-xs lg:min-h-full'>
        {/* 背景科技星芒微网格 */}
        <div className='pointer-events-none absolute inset-0 bg-[radial-gradient(ellipse_at_center,color-mix(in_oklch,var(--primary)_6%,transparent),transparent_70%)]' />
        <div className='pointer-events-none absolute inset-0 [background-image:linear-gradient(to_right,currentColor_1px,transparent_1px),linear-gradient(to_bottom,currentColor_1px,transparent_1px)] [background-size:24px_24px] opacity-[0.02] dark:opacity-[0.05]' />

        {/* 科技扫描雷达光环 */}
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
        <div className='relative mt-8 grid w-full max-w-2xl grid-cols-1 gap-3 sm:grid-cols-3'>
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
      </Card>
    )
  }

  // 检索完成但无匹配人员
  if (results.length === 0) {
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
      </Card>
    )
  }

  // 命中匹配结果列表展示
  return (
    <div className='flex h-full flex-1 flex-col gap-4'>
      {/* 结果栏 Header */}
      <div className='flex flex-wrap items-center justify-between gap-2 rounded-xl border border-border/70 bg-card/60 px-4 py-2.5 shadow-xs backdrop-blur-xs'>
        <div className='flex items-center gap-2.5'>
          <div className='flex h-7 w-7 items-center justify-center rounded-lg bg-primary/10 text-primary'>
            <Layers className='h-4 w-4' />
          </div>
          <div>
            <h3 className='text-sm font-bold text-foreground'>
              {t('results.title')}
            </h3>
            <span className='text-xs text-muted-foreground'>
              {t('results.count', { count: results.length })}
            </span>
          </div>
        </div>

        {/* 关键置信度统计指标 */}
        <div className='flex items-center gap-2 text-xs'>
          <Badge
            variant='outline'
            className='border-emerald-500/30 bg-emerald-500/10 px-2 py-0.5 text-[11px] font-semibold text-emerald-600 dark:text-emerald-400'
          >
            {t('results.card.rankTop')}
          </Badge>
        </div>
      </div>

      {/* 结果列表画布底托（纵向列表流式排列，高度自适应） */}
      <div className='flex h-full min-h-[420px] flex-1 flex-col rounded-2xl border border-border/70 bg-card/40 p-4 shadow-xs backdrop-blur-xs lg:min-h-full'>
        <div className='flex flex-col gap-2.5'>
          {results.map((match, idx) => (
            <ImageSearchMatchRow
              key={`${match.personId}-${match.matchedFaceId}`}
              match={match}
              rank={idx + 1}
            />
          ))}
        </div>
      </div>
    </div>
  )
}
