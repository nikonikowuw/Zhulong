import { ZoomIn } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { statusLevelClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { type FaceCandidate } from '../../data/schema'
import { MediaImage } from '../common/media-image'
import { getGalleryImageUrl } from '../common/media-utils'

interface CandidateComparisonListProps {
  candidates: FaceCandidate[]
  matched: boolean
  matchedFaceId: string
  selectedCandidateIdx: number | null
  onSelectCandidateIdx: (idx: number | null) => void
  onOpenPreviewById: (id: string) => void
}

export function CandidateComparisonList({
  candidates,
  matched,
  matchedFaceId,
  selectedCandidateIdx,
  onSelectCandidateIdx,
  onOpenPreviewById,
}: CandidateComparisonListProps) {
  const { t } = useTranslation('records')

  if (!candidates || candidates.length === 0) return null

  return (
    <div className='space-y-2 pt-1'>
      <div className='flex items-center justify-between gap-2 text-xs font-medium text-muted-foreground'>
        <span>{t('detail.candidates')}</span>
        <span className='font-mono text-[11px]'>({candidates.length})</span>
      </div>
      <p className='text-[11px] text-muted-foreground/80'>
        {t('detail.candidatesHint')}
      </p>
      <div className='grid grid-cols-1 gap-2'>
        {candidates.map((cand, idx) => {
          const candSim = Math.max(
            0,
            Math.min(100, Math.round(cand.similarity * 100))
          )
          const isSelected = selectedCandidateIdx === idx
          const isMatchedItem =
            matched && Boolean(cand.faceId) && cand.faceId === matchedFaceId
          const candName =
            cand.personName ||
            cand.personId ||
            t('detail.candidatePreview', { rank: idx + 1 })
          const candSubId =
            cand.personId && cand.faceId
              ? `${cand.personId} · ${cand.faceId}`
              : cand.faceId || cand.personId
          const candPhotoUrl = getGalleryImageUrl(cand.personRowId, cand.faceId)

          return (
            <div
              key={`cand-${cand.faceId || cand.personId || idx}`}
              className={cn(
                'relative flex w-full items-center gap-3 overflow-hidden rounded-md border p-2 text-xs transition-colors',
                isSelected
                  ? 'border-primary bg-primary/5 ring-1 ring-primary/30'
                  : 'border-border/80 bg-muted/30 hover:bg-muted/50'
              )}
            >
              <div className='group relative size-12 shrink-0 overflow-hidden rounded-md border border-border bg-muted/40 shadow-xs'>
                <MediaImage
                  src={candPhotoUrl}
                  alt={candName}
                  className='size-full'
                  imageClassName='object-cover'
                  fallbackText=''
                />
                {candPhotoUrl && (
                  <button
                    type='button'
                    onClick={() => onOpenPreviewById(`cand-${idx}`)}
                    aria-label={t('detail.previewImageLabel', {
                      name: candName,
                    })}
                    className='absolute inset-0 flex cursor-zoom-in items-center justify-center bg-black/0 text-white opacity-0 transition-all group-hover:bg-black/40 group-hover:opacity-100 focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-primary focus-visible:outline-hidden'
                  >
                    <ZoomIn className='size-3.5 drop-shadow-sm' />
                  </button>
                )}
              </div>

              <button
                type='button'
                aria-pressed={isSelected}
                onClick={() => onSelectCandidateIdx(isSelected ? null : idx)}
                className='min-w-0 flex-1 space-y-1 text-start focus-visible:ring-2 focus-visible:ring-primary focus-visible:outline-hidden'
              >
                <div className='flex items-center justify-between gap-2'>
                  <div className='flex min-w-0 items-center gap-1.5'>
                    <span className='shrink-0 font-mono text-[10px] font-semibold text-muted-foreground'>
                      #{idx + 1}
                    </span>
                    <span className='truncate font-semibold text-foreground'>
                      {candName}
                    </span>
                    {isMatchedItem && (
                      <Badge
                        variant='outline'
                        className={cn(
                          'shrink-0 px-1.5 py-0 text-[10px]',
                          statusLevelClass('active')
                        )}
                      >
                        {t('detail.matchedCandidate')}
                      </Badge>
                    )}
                  </div>
                  <div className='flex shrink-0 items-center gap-1'>
                    {isSelected && (
                      <Badge
                        variant='default'
                        className='px-1.5 py-0 text-[10px]'
                      >
                        {t('detail.comparing')}
                      </Badge>
                    )}
                    <Badge
                      variant='secondary'
                      className='font-mono text-[11px] font-semibold'
                    >
                      {candSim}%
                    </Badge>
                  </div>
                </div>

                <div className='truncate font-mono text-[10px] text-muted-foreground'>
                  {candSubId}
                </div>

                <div className='h-1 w-full overflow-hidden rounded-full bg-muted'>
                  <div
                    className='h-full rounded-full bg-primary/70 transition-all'
                    style={{ width: `${candSim}%` }}
                  />
                </div>
              </button>
            </div>
          )
        })}
      </div>
    </div>
  )
}
