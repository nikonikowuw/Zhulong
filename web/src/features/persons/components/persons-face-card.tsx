import { memo, useState } from 'react'
import {
  CheckCircle2,
  Expand,
  Image as ImageIcon,
  Loader2,
  Star,
  Trash2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatLocalDateTime } from '@/lib/csv'
import { getGalleryImageUrl } from '@/lib/media-url'
import { statusLevelClass, statusTextClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { type Face } from '../data/schema'

/**
 * 单张人脸底库卡片。
 *
 * 从 `persons-faces-dialog` 拆出：那个文件曾同时承担 Sheet 外壳、人员信息
 * 栏、批量上传编排与卡片渲染，648 行远超 §1.2 的 300 行红线。卡片是纯展示
 * + 三个回调，没有理由和上传编排耦在同一个模块里。
 */
export interface FaceCardProps {
  face: Face
  personId: string
  isPrimary: boolean
  isDeleting: boolean
  isSettingPrimary: boolean
  onDelete: (faceId: string) => void
  onSetPrimary: (faceId: string) => void
  onPreview: (imageUrl: string, title: string) => void
}

export const FaceCard = memo(function FaceCard({
  face,
  personId,
  isPrimary,
  isDeleting,
  isSettingPrimary,
  onDelete,
  onSetPrimary,
  onPreview,
}: FaceCardProps) {
  const { t } = useTranslation('persons')
  const [imgError, setImgError] = useState(false)
  const imageUrl = getGalleryImageUrl(personId, face.id)

  const displayName = face.imagePath
    ? face.imagePath.split('/').pop() || face.imagePath
    : face.id

  return (
    <div
      data-testid='face-card'
      title={displayName}
      className={cn(
        'group flex flex-col justify-between overflow-hidden rounded-xl border bg-card shadow-2xs transition-all duration-200 select-none hover:shadow-md',
        // 主人脸 = §1.3.3 的 active 级别，只提升边框不抢卡片底色。
        isPrimary
          ? 'border-teal-500/80 ring-2 ring-teal-500/20 dark:border-teal-400/80 dark:ring-teal-400/20'
          : 'border-border hover:border-primary/40'
      )}
    >
      {/* 照片展示区 */}
      <div className='relative aspect-square w-full overflow-hidden bg-muted/40'>
        {!imgError ? (
          <button
            type='button'
            onClick={() => onPreview(imageUrl, displayName)}
            aria-label={t('imagePreview.enterFullscreen')}
            className='h-full w-full cursor-zoom-in'
          >
            <img
              src={imageUrl}
              alt={displayName}
              onError={() => setImgError(true)}
              className='h-full w-full object-cover transition-transform duration-300 group-hover:scale-105'
              loading='lazy'
              decoding='async'
            />
          </button>
        ) : (
          <div className='flex h-full w-full flex-col items-center justify-center gap-1.5 p-2 text-center text-muted-foreground'>
            <ImageIcon className='h-6 w-6 text-muted-foreground/50' />
            <span className='line-clamp-1 font-mono text-[10px] text-muted-foreground/70'>
              {displayName}
            </span>
          </div>
        )}

        {/* 主人脸角标 */}
        {isPrimary && (
          <div className='pointer-events-none absolute start-2 top-2 z-10'>
            <Badge
              variant='outline'
              className={cn(
                'gap-1 px-2 py-0.5 text-[10px] font-medium backdrop-blur-xs',
                statusLevelClass('active')
              )}
            >
              <Star className='h-3 w-3' />
              <span>{t('fields.primaryFace')}</span>
            </Badge>
          </div>
        )}

        {/* 悬浮快捷放大预览按钮 */}
        <div className='absolute end-2 top-2 z-10 opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100'>
          <Button
            type='button'
            variant='secondary'
            size='icon'
            onClick={() => onPreview(imageUrl, displayName)}
            className='h-7 w-7 rounded-full bg-background/85 shadow-xs backdrop-blur-xs hover:bg-background'
            title={t('imagePreview.enterFullscreen')}
            aria-label={t('imagePreview.enterFullscreen')}
          >
            <Expand className='h-3.5 w-3.5' />
          </Button>
        </div>
      </div>

      {/* 底部信息与常驻操作栏 */}
      <div className='flex flex-col gap-1.5 border-t border-border/50 bg-card p-2.5'>
        <div className='flex items-center justify-between text-[11px] text-muted-foreground'>
          <span className='truncate font-mono' title={displayName}>
            {imgError ? `ID: ${face.id}` : displayName}
          </span>
          <span className='shrink-0 text-[10px]'>
            {formatLocalDateTime(face.createdAt)}
          </span>
        </div>

        <div className='flex items-center justify-between gap-1 pt-0.5'>
          {!isPrimary ? (
            <Button
              type='button'
              size='sm'
              variant='outline'
              disabled={isSettingPrimary || isDeleting}
              onClick={() => onSetPrimary(face.id)}
              className='h-7 flex-1 gap-1 text-[11px] font-normal hover:bg-muted'
              title={t('actions.setPrimary')}
            >
              {isSettingPrimary ? (
                <Loader2 className='h-3 w-3 animate-spin' />
              ) : (
                <Star className='h-3 w-3 text-muted-foreground' />
              )}
              <span>{t('actions.setPrimary')}</span>
            </Button>
          ) : (
            <div
              className={cn(
                'flex h-7 flex-1 items-center gap-1 text-[11px] font-medium',
                statusTextClass('active')
              )}
            >
              <CheckCircle2 className='h-3.5 w-3.5' />
              <span className='font-mono text-[10px] text-muted-foreground/80'>
                {face.id}
              </span>
            </div>
          )}

          <Button
            type='button'
            variant='ghost'
            size='icon'
            disabled={isDeleting || isSettingPrimary}
            onClick={() => onDelete(face.id)}
            className='h-7 w-7 text-muted-foreground hover:bg-destructive/10 hover:text-destructive'
            aria-label={t('actions.deleteFace')}
            title={t('actions.deleteFace')}
          >
            {isDeleting ? (
              <Loader2 className='h-3.5 w-3.5 animate-spin' />
            ) : (
              <Trash2 className='h-3.5 w-3.5' />
            )}
          </Button>
        </div>
      </div>
    </div>
  )
})
