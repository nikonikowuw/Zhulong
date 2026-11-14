import { Check, CheckCircle2, Copy, Sparkles, ZoomIn } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { statusDotClass, statusLevelClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { type Recognition } from '../../data/schema'
import { MediaImage } from '../common/media-image'
import { getGalleryImageUrl } from '../common/media-utils'
import { CandidateComparisonList } from './candidate-comparison-list'
import { useCopyFeedback } from './record-detail-helpers'
import { type RecordPreviewImageItem } from './record-image-preview-dialog'

interface RecognitionComparisonPanelProps {
  record: Recognition
  selectedCandidateIdx: number | null
  onSelectCandidateIdx: (idx: number | null) => void
  previewImages: RecordPreviewImageItem[]
  onOpenPreviewById: (id: string) => void
  onOpenPreviewIndex: (index: number) => void
}

export function RecognitionComparisonPanel({
  record: rec,
  selectedCandidateIdx,
  onSelectCandidateIdx,
  previewImages,
  onOpenPreviewById,
  onOpenPreviewIndex,
}: RecognitionComparisonPanelProps) {
  const { t } = useTranslation('records')
  const externalIdCopy = useCopyFeedback()

  const activeCandidate =
    selectedCandidateIdx !== null &&
    selectedCandidateIdx >= 0 &&
    selectedCandidateIdx < rec.candidates.length
      ? rec.candidates[selectedCandidateIdx]
      : null

  const activeGalleryUrl = activeCandidate
    ? getGalleryImageUrl(activeCandidate.personRowId, activeCandidate.faceId)
    : getGalleryImageUrl(rec.personId, rec.matchedFaceId)

  const openActiveGalleryPreview = () => {
    if (!activeGalleryUrl) return
    if (activeCandidate && selectedCandidateIdx !== null) {
      onOpenPreviewById(`cand-${selectedCandidateIdx}`)
      return
    }
    const byMatched = previewImages.findIndex(
      (item) => item.id === 'matched-gallery' || item.url === activeGalleryUrl
    )
    if (byMatched !== -1) onOpenPreviewIndex(byMatched)
  }

  return (
    <div className='space-y-4 rounded-lg border border-border bg-card p-4 shadow-xs'>
      <div className='flex items-center justify-between'>
        <h4 className='flex items-center gap-1.5 text-xs font-semibold tracking-wider text-muted-foreground uppercase'>
          <Sparkles className='size-3.5 text-primary' />
          {t('detail.comparison')}
        </h4>
        {rec.matched && rec.similarity > 0 && (
          <Badge
            variant='outline'
            className={cn('font-mono text-xs', statusLevelClass('active'))}
          >
            <span
              className={cn(
                'me-1 size-1.5 rounded-full',
                statusDotClass('active')
              )}
            />
            {t('card.similarity')}: {Math.round(rec.similarity * 100)}%
          </Badge>
        )}
      </div>

      {/* 比对工作台 */}
      <div className='relative grid grid-cols-2 gap-4 rounded-lg border border-border/60 bg-muted/20 p-3'>
        <div className='flex flex-col items-center space-y-2'>
          <span className='text-[11px] font-medium text-muted-foreground'>
            {t('detail.snapshot')}
          </span>
          <div className='group relative size-24 overflow-hidden rounded-md border border-border bg-muted/40 shadow-xs'>
            <MediaImage
              path={rec.faceImagePath}
              alt={t('detail.snapshot')}
              className='size-full'
              imageClassName='object-contain'
              fallbackText={t('card.fallbackFace')}
            />
            {rec.faceImagePath && (
              <button
                type='button'
                onClick={() => onOpenPreviewById('face-crop')}
                aria-label={t('detail.previewImageLabel', {
                  name: t('detail.snapshot'),
                })}
                className='absolute inset-0 flex cursor-zoom-in items-center justify-center bg-black/0 text-white opacity-0 transition-all group-hover:bg-black/35 group-hover:opacity-100 focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-primary focus-visible:outline-hidden'
              >
                <ZoomIn className='size-4 drop-shadow-sm' />
              </button>
            )}
          </div>
        </div>

        <div className='flex flex-col items-center space-y-2'>
          <div className='flex min-h-4 items-center gap-1.5'>
            <span className='max-w-28 truncate text-[11px] font-medium text-muted-foreground'>
              {activeCandidate && selectedCandidateIdx !== null
                ? `${t('detail.candidatePreview', { rank: selectedCandidateIdx + 1 })} · ${activeCandidate.personName || activeCandidate.personId || `#${selectedCandidateIdx + 1}`}`
                : t('detail.gallery')}
            </span>
            {activeCandidate && (
              <Button
                type='button'
                variant='ghost'
                size='sm'
                className='h-4 px-1 text-[10px] text-primary hover:text-primary'
                onClick={() => onSelectCandidateIdx(null)}
              >
                {t('detail.resetComparison')}
              </Button>
            )}
          </div>
          <div
            className={cn(
              'group relative size-24 overflow-hidden rounded-md border bg-muted/40 shadow-xs transition-colors',
              activeCandidate
                ? 'border-primary/70 ring-1 ring-primary/30'
                : 'border-border'
            )}
          >
            <MediaImage
              src={activeGalleryUrl}
              alt={
                activeCandidate && selectedCandidateIdx !== null
                  ? activeCandidate.personName ||
                    activeCandidate.personId ||
                    t('detail.candidatePreview', {
                      rank: selectedCandidateIdx + 1,
                    })
                  : t('detail.gallery')
              }
              className='size-full'
              imageClassName='object-contain'
              fallbackText={t('detail.noGallery')}
            />
            {activeGalleryUrl && (
              <button
                type='button'
                onClick={openActiveGalleryPreview}
                aria-label={t('detail.previewImageLabel', {
                  name:
                    activeCandidate && selectedCandidateIdx !== null
                      ? activeCandidate.personName ||
                        activeCandidate.personId ||
                        t('detail.candidatePreview', {
                          rank: selectedCandidateIdx + 1,
                        })
                      : t('detail.gallery'),
                })}
                className='absolute inset-0 flex cursor-zoom-in items-center justify-center bg-black/0 text-white opacity-0 transition-all group-hover:bg-black/35 group-hover:opacity-100 focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-primary focus-visible:outline-hidden'
              >
                <ZoomIn className='size-4 drop-shadow-sm' />
              </button>
            )}
          </div>
          {activeCandidate && (
            <Badge variant='secondary' className='font-mono text-[10px]'>
              {Math.round(activeCandidate.similarity * 100)}%
            </Badge>
          )}
        </div>
      </div>

      {/* 身份核验结果 */}
      {rec.matched ? (
        <div
          className={cn(
            'rounded-md border p-3 text-xs',
            statusLevelClass('active')
          )}
        >
          <div className='flex items-center justify-between'>
            <div className='flex items-center gap-1.5 font-semibold text-foreground'>
              <CheckCircle2 className='size-4 text-emerald-500' />
              <span>{rec.personName || t('card.matched')}</span>
            </div>
            {rec.personExternalId && (
              <Button
                type='button'
                variant='ghost'
                size='sm'
                className='h-6 px-1.5 text-[11px] text-muted-foreground hover:text-foreground'
                onClick={() => void externalIdCopy.copy(rec.personExternalId)}
              >
                {externalIdCopy.copied ? (
                  <Check className='me-1 size-3 text-emerald-500' />
                ) : (
                  <Copy className='me-1 size-3' />
                )}
                {rec.personExternalId}
              </Button>
            )}
          </div>
          {rec.personId && (
            <div className='mt-1 font-mono text-[11px] text-muted-foreground'>
              {t('detail.personId')}: {rec.personId}
            </div>
          )}
        </div>
      ) : (
        <div className='rounded-md border border-border bg-muted/30 p-2.5 text-center text-xs text-muted-foreground'>
          {t('detail.verificationUnmatched')}
        </div>
      )}

      <CandidateComparisonList
        candidates={rec.candidates}
        matched={rec.matched}
        matchedFaceId={rec.matchedFaceId}
        selectedCandidateIdx={selectedCandidateIdx}
        onSelectCandidateIdx={onSelectCandidateIdx}
        onOpenPreviewById={onOpenPreviewById}
      />
    </div>
  )
}
