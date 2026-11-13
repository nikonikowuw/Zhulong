import { STATUS_LEVEL_CLASS, STATUS_TEXT_CLASS } from '@/lib/status-palette'

export const DEFAULT_IMAGE_SEARCH_THRESHOLD = 0.7
export const DEFAULT_IMAGE_SEARCH_TOP_K = 10
export const LOWER_RETRY_THRESHOLD = 0.5

const HIGH_MATCH_THRESHOLD = 0.85
const MEDIUM_MATCH_THRESHOLD = 0.7

export function getSimilarityPresentation(similarity: number) {
  const similarityNum = similarity * 100
  const percentWidth = Math.max(0, Math.min(similarityNum, 100))
  const similarityPercent = similarityNum.toFixed(1)
  const isHighMatch = similarity >= HIGH_MATCH_THRESHOLD
  const isMediumMatch = similarity >= MEDIUM_MATCH_THRESHOLD

  if (isHighMatch) {
    return {
      percentWidth,
      similarityPercent,
      isHighMatch: true,
      isMediumMatch: true,
      badgeClass: STATUS_LEVEL_CLASS.active,
      panelClass: STATUS_LEVEL_CLASS.active,
      textClass: STATUS_TEXT_CLASS.active,
      barClass: 'bg-gradient-to-r from-emerald-500 to-teal-400',
    }
  }

  if (isMediumMatch) {
    return {
      percentWidth,
      similarityPercent,
      isHighMatch: false,
      isMediumMatch: true,
      badgeClass:
        'border-primary/30 bg-primary/10 text-primary dark:border-primary/40 dark:bg-primary/20',
      panelClass:
        'border-primary/30 bg-primary/10 text-foreground dark:border-primary/40 dark:bg-primary/20',
      textClass: 'text-primary',
      barClass: 'bg-gradient-to-r from-primary/80 to-primary',
    }
  }

  return {
    percentWidth,
    similarityPercent,
    isHighMatch: false,
    isMediumMatch: false,
    badgeClass: STATUS_LEVEL_CLASS.warning,
    panelClass: STATUS_LEVEL_CLASS.warning,
    textClass: STATUS_TEXT_CLASS.warning,
    barClass: 'bg-gradient-to-r from-amber-500 to-amber-400',
  }
}
