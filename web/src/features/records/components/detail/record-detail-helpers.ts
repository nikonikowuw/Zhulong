import { useCallback, useEffect, useRef, useState } from 'react'
import { type TFunction } from 'i18next'
import { copyToClipboard } from '@/lib/clipboard'
import {
  isAlarmRecord,
  isCaptureRecord,
  isRecognitionRecord,
  type RecordItem,
  type RecordsTab,
} from '../../data/schema'
import { getGalleryImageUrl, getMediaUrl } from '../common/media-utils'
import { type RecordPreviewImageItem } from './record-image-preview-dialog'

/** 拟真车牌徽章样式（符合行业视觉习惯并兼容暗黑模式） */
export function getPlateBadgeStyle(color?: string): string {
  const c = color?.toLowerCase() || ''
  if (c.includes('绿') || c.includes('green')) {
    return 'border-emerald-500 bg-gradient-to-r from-emerald-600 to-green-500 text-white shadow-emerald-950/20'
  }
  if (c.includes('黄') || c.includes('yellow')) {
    return 'border-amber-500 bg-amber-400 font-black text-neutral-950 shadow-amber-950/20'
  }
  if (c.includes('白') || c.includes('white')) {
    return 'border-neutral-300 bg-neutral-100 font-bold text-neutral-950 shadow-neutral-950/20'
  }
  if (c.includes('黑') || c.includes('black')) {
    return 'border-neutral-700 bg-neutral-900 font-bold text-white shadow-black/40'
  }
  return 'border-blue-500 bg-blue-600 text-white shadow-blue-950/20'
}

/** 带卸载/连击保护的复制反馈 Hook */
export function useCopyFeedback(durationMs = 2000) {
  const [copied, setCopied] = useState(false)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    return () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current)
      }
    }
  }, [])

  const copy = useCallback(
    async (text: string) => {
      if (!text) return
      const success = await copyToClipboard(text)
      if (!success) return
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current)
      }
      setCopied(true)
      timerRef.current = setTimeout(() => {
        setCopied(false)
        timerRef.current = null
      }, durationMs)
    },
    [durationMs]
  )

  return { copied, copy }
}

/** 汇总当前记录可全屏查看的图片序列，支持跨图左右切换与缩略图跳转 */
export function buildPreviewImages(
  record: RecordItem,
  tab: RecordsTab,
  t: TFunction<'records'>
): RecordPreviewImageItem[] {
  const previewImages: RecordPreviewImageItem[] = []
  const rec = isRecognitionRecord(record) ? record : null
  const alm = isAlarmRecord(record) ? record : null
  const cap = isCaptureRecord(record) ? record : null
  const isVehicleCapture = cap !== null && tab === 'vehicles'

  if (record.imagePath) {
    previewImages.push({
      id: 'full',
      url: getMediaUrl(record.imagePath),
      title: t('detail.fullImage'),
    })
  }

  if (rec) {
    if (rec.faceImagePath) {
      previewImages.push({
        id: 'face-crop',
        url: getMediaUrl(rec.faceImagePath),
        title: t('detail.snapshot'),
      })
    }
    const matchedGalleryUrl = getGalleryImageUrl(
      rec.personId,
      rec.matchedFaceId
    )
    const candidatePhotoItems: RecordPreviewImageItem[] = []
    rec.candidates.forEach((cand, idx) => {
      const url = getGalleryImageUrl(cand.personRowId, cand.faceId)
      if (!url) return
      const name =
        cand.personName ||
        cand.personId ||
        t('detail.candidatePreview', { rank: idx + 1 })
      candidatePhotoItems.push({
        id: `cand-${idx}`,
        url,
        title: `${t('detail.candidatePreview', { rank: idx + 1 })} · ${name}`,
        subtitle: `${Math.max(0, Math.min(100, Math.round(cand.similarity * 100)))}%`,
      })
    })
    if (
      matchedGalleryUrl &&
      !candidatePhotoItems.some((item) => item.url === matchedGalleryUrl)
    ) {
      previewImages.push({
        id: 'matched-gallery',
        url: matchedGalleryUrl,
        title: t('detail.gallery'),
        subtitle: rec.personName || rec.personExternalId || undefined,
      })
    }
    previewImages.push(...candidatePhotoItems)
  }

  if (alm && alm.cropPath) {
    previewImages.push({
      id: 'alarm-crop',
      url: getMediaUrl(alm.cropPath),
      title: t('detail.snapshot'),
      subtitle: alm.alarmType,
    })
  }

  if (cap && cap.cropPath) {
    previewImages.push({
      id: 'capture-crop',
      url: getMediaUrl(cap.cropPath),
      title: isVehicleCapture ? t('detail.vehicleCrop') : t('detail.snapshot'),
    })
  }

  return previewImages
}
