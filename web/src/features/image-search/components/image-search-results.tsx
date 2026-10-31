import { AlertCircle, Inbox, ScanFace } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { type ImageSearchMatchInfo } from '../data/schema'
import { ImageSearchMatchCard } from './image-search-match-card'

interface ImageSearchResultsProps {
  results: ImageSearchMatchInfo[] | null
  isSearching: boolean
  isError: boolean
  error: Error | null
  topK: number
}

export function ImageSearchResults({
  results,
  isSearching,
  isError,
  error,
  topK,
}: ImageSearchResultsProps) {
  const { t } = useTranslation('imageSearch')

  if (isSearching) {
    return (
      <div className='flex flex-col gap-3'>
        <div className='flex items-center justify-between'>
          <Skeleton className='h-5 w-32' />
          <Skeleton className='h-4 w-20' />
        </div>
        <div className='grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4'>
          {Array.from({ length: Math.min(topK, 8) }).map((_, i) => (
            <Card key={i} className='flex flex-col gap-3 p-4'>
              <div className='flex gap-3'>
                <Skeleton className='h-20 w-20 shrink-0 rounded-lg' />
                <div className='flex flex-1 flex-col justify-between py-1'>
                  <Skeleton className='h-4 w-3/4' />
                  <Skeleton className='h-3 w-1/2' />
                  <Skeleton className='h-4 w-1/3' />
                </div>
              </div>
              <Skeleton className='h-7 w-full' />
            </Card>
          ))}
        </div>
      </div>
    )
  }

  if (isError) {
    return (
      <Card className='flex min-h-[260px] flex-col items-center justify-center border-destructive/30 bg-destructive/5 p-8 text-center'>
        <div className='mb-3 flex h-12 w-12 items-center justify-center rounded-full bg-destructive/10 text-destructive'>
          <AlertCircle className='h-6 w-6' />
        </div>
        <h3 className='text-sm font-semibold text-destructive'>
          {t('results.errorTitle')}
        </h3>
        <p className='mt-1 max-w-md text-xs text-muted-foreground'>
          {error?.message || t('results.emptyDescription')}
        </p>
      </Card>
    )
  }

  if (results === null) {
    return (
      <Card className='flex min-h-[300px] flex-col items-center justify-center border-border/80 bg-card/40 p-8 text-center'>
        <div className='mb-3 flex h-12 w-12 items-center justify-center rounded-full bg-primary/10 text-primary'>
          <ScanFace className='h-6 w-6' />
        </div>
        <h3 className='text-sm font-semibold text-foreground'>
          {t('results.idleTitle')}
        </h3>
        <p className='mt-1 max-w-md text-xs text-muted-foreground'>
          {t('results.idleDescription')}
        </p>
      </Card>
    )
  }

  if (results.length === 0) {
    return (
      <Card className='flex min-h-[300px] flex-col items-center justify-center border-border/80 bg-card/40 p-8 text-center'>
        <div className='mb-3 flex h-12 w-12 items-center justify-center rounded-full bg-muted text-muted-foreground'>
          <Inbox className='h-6 w-6' />
        </div>
        <h3 className='text-sm font-semibold text-foreground'>
          {t('results.emptyTitle')}
        </h3>
        <p className='mt-1 max-w-md text-xs text-muted-foreground'>
          {t('results.emptyDescription')}
        </p>
      </Card>
    )
  }

  return (
    <div className='flex flex-col gap-3'>
      <div className='flex items-center justify-between'>
        <h3 className='text-sm font-semibold text-foreground'>
          {t('results.title')}
        </h3>
        <span className='text-xs text-muted-foreground'>
          {t('results.count', { count: results.length })}
        </span>
      </div>

      <div className='grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4'>
        {results.map((match, idx) => (
          <ImageSearchMatchCard
            key={`${match.personId}-${match.matchedFaceId}`}
            match={match}
            rank={idx + 1}
          />
        ))}
      </div>
    </div>
  )
}
