import { useCallback, useRef, useState } from 'react'
import {
  Copy,
  Edit2,
  Image as ImageIcon,
  Loader2,
  Plus,
  Upload,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { copyToClipboard } from '@/lib/clipboard'
import { formatLocalDateTime } from '@/lib/csv'
import { errorMessageOf } from '@/lib/handle-server-error'
import { getGalleryImageUrl } from '@/lib/media-url'
import { cn } from '@/lib/utils'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import { useFaceBatchUpload } from '../hooks/use-face-batch-upload'
import {
  useDeleteFace,
  usePerson,
  usePersonFaces,
  useSetPrimaryFace,
} from '../hooks/use-persons'
import {
  FACE_IMAGE_ACCEPT,
  initialOf,
  primaryFaceIdOf,
} from '../persons-helpers'
import { FaceCard } from './persons-face-card'
import { usePersonsContext } from './persons-provider'

export function PersonsFacesDialog() {
  const { t } = useTranslation('persons')
  const { open, setOpen, currentRow, setCurrentRow, setPreviewImage } =
    usePersonsContext()
  const [deletingId, setDeletingId] = useState<string | null>(null)
  const [settingPrimaryId, setSettingPrimaryId] = useState<string | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const isOpen = open === 'faces' && Boolean(currentRow)
  const personId = currentRow?.id

  // 订阅详情 query：删除主脸、设主脸或修改姓名后，后端可能自动顺延主图或变更
  // 展示字段，仅靠列表打开时的 `currentRow` 快照会导致显示过期甚至 404。
  const { data: personDetail } = usePerson(isOpen ? personId : undefined)
  const activePerson = personDetail ?? currentRow

  const {
    data: facePage,
    isLoading: isLoadingFaces,
    isError: isFacesError,
    refetch: refetchFaces,
  } = usePersonFaces(isOpen ? personId : undefined)
  const faces = facePage?.items ?? []
  const faceTotal = facePage?.total ?? 0

  const deleteFaceMutation = useDeleteFace({ suppressGlobalToast: true })
  const setPrimaryMutation = useSetPrimaryFace({ suppressGlobalToast: true })

  const handleFirstFaceEnrolled = useCallback(
    (faceId: string) => {
      setCurrentRow((prev) =>
        prev ? { ...prev, primaryFaceId: faceId } : prev
      )
    },
    [setCurrentRow]
  )

  const {
    isDragOver,
    setIsDragOver,
    isUploadingBatch,
    batchProgress,
    uploadFiles,
  } = useFaceBatchUpload({
    personId,
    hasExistingFaces: faces.length > 0,
    onFirstFaceEnrolled: handleFirstFaceEnrolled,
  })

  const handleCopyPersonId = async () => {
    const value = activePerson?.personId
    if (!value) return
    const ok = await copyToClipboard(value)
    toast[ok ? 'success' : 'error'](
      ok ? t('sheet.idCopied') : t('sheet.idCopyFailed')
    )
  }

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files) {
      void uploadFiles(e.target.files)
    }
    e.target.value = ''
  }

  const handleDrop = (e: React.DragEvent<HTMLButtonElement>) => {
    e.preventDefault()
    setIsDragOver(false)
    if (e.dataTransfer.files) {
      void uploadFiles(e.dataTransfer.files)
    }
  }

  const handleSetPrimary = async (faceId: string) => {
    if (!personId) return
    setSettingPrimaryId(faceId)
    try {
      await setPrimaryMutation.mutateAsync({ personId, faceId })
      setCurrentRow((prev) =>
        prev ? { ...prev, primaryFaceId: faceId } : prev
      )
      toast.success(t('notifications.setPrimarySuccess'))
    } catch (err: unknown) {
      toast.error(errorMessageOf(err))
    } finally {
      setSettingPrimaryId(null)
    }
  }

  const handleDeleteFace = async (faceId: string) => {
    if (!personId) return
    setDeletingId(faceId)
    try {
      await deleteFaceMutation.mutateAsync({ personId, faceId })
      // 若删掉的是当前主人脸，在详情 refetch 完成前先同步回落到剩余第一张或清空，
      // 防止抽屉顶部头像继续用已删除 ID 发请求产生 404。
      if (activePerson?.primaryFaceId === faceId) {
        const nextFaceId = faces.find((f) => f.id !== faceId)?.id ?? null
        setCurrentRow((prev) =>
          prev ? { ...prev, primaryFaceId: nextFaceId } : prev
        )
      }
      toast.success(t('notifications.deleteFaceSuccess'))
    } catch (err: unknown) {
      toast.error(errorMessageOf(err))
    } finally {
      setDeletingId(null)
    }
  }

  const handlePreview = useCallback(
    (url: string, title: string) => {
      setPreviewImage({ url, title })
    },
    [setPreviewImage]
  )

  const handleClose = () => {
    if (isUploadingBatch || deletingId) return
    setOpen(null)
  }

  // 主人脸 ID：faces 加载完后校验 `primaryFaceId` 是否仍在当前集合内，避免
  // 快照残留已删 ID；加载中回落人员快照字段。
  const effectivePrimaryFaceId = isLoadingFaces
    ? activePerson
      ? primaryFaceIdOf(activePerson)
      : undefined
    : activePerson?.primaryFaceId &&
        faces.some((f) => f.id === activePerson.primaryFaceId)
      ? activePerson.primaryFaceId
      : faces[0]?.id

  const avatarUrl = getGalleryImageUrl(personId, effectivePrimaryFaceId)
  const fallbackChar = initialOf(activePerson?.name)

  return (
    <Sheet open={isOpen} onOpenChange={(v) => !v && handleClose()}>
      <SheetContent
        side='right'
        className='flex h-full w-full flex-col gap-0 p-0 sm:max-w-xl md:max-w-2xl'
      >
        <SheetHeader className='border-b border-border px-6 py-4'>
          <div className='space-y-1 pe-6'>
            <SheetTitle className='text-lg font-semibold tracking-tight'>
              {t('dialogs.facesTitle', { name: activePerson?.name || '' })}
            </SheetTitle>
            <SheetDescription className='text-xs text-muted-foreground'>
              {t('dialogs.facesDesc')}
            </SheetDescription>
          </div>
        </SheetHeader>

        {/* 人员详细信息卡片 Bar */}
        <div className='flex items-center justify-between gap-4 border-b border-border/60 bg-muted/20 px-6 py-4'>
          <div className='flex items-center gap-3.5'>
            <Avatar className='h-12 w-12 rounded-full border-2 border-border shadow-xs'>
              {avatarUrl && (
                <AvatarImage
                  src={avatarUrl}
                  alt={activePerson?.name}
                  className='object-cover'
                />
              )}
              <AvatarFallback className='bg-primary/10 text-sm font-semibold text-primary'>
                {fallbackChar}
              </AvatarFallback>
            </Avatar>
            <div className='space-y-1'>
              <div className='flex items-center gap-2'>
                <span className='text-base font-semibold text-foreground'>
                  {activePerson?.name}
                </span>
                {activePerson?.personId ? (
                  <Badge
                    variant='secondary'
                    className='gap-1 font-mono text-[11px] text-muted-foreground'
                  >
                    <span>{activePerson.personId}</span>
                    <button
                      type='button'
                      onClick={() => void handleCopyPersonId()}
                      className='cursor-pointer hover:text-foreground'
                      title={t('sheet.copyId')}
                      aria-label={t('sheet.copyId')}
                    >
                      <Copy className='h-3 w-3' />
                    </button>
                  </Badge>
                ) : (
                  <Badge
                    variant='outline'
                    className='text-[10px] text-muted-foreground'
                  >
                    {t('sheet.noPersonId')}
                  </Badge>
                )}
              </div>
              <p className='text-xs text-muted-foreground'>
                {t('fields.createdAt')}:{' '}
                {activePerson?.createdAt
                  ? formatLocalDateTime(activePerson.createdAt)
                  : '-'}
              </p>
            </div>
          </div>

          <Button
            type='button'
            size='sm'
            variant='outline'
            onClick={() => {
              if (personDetail) setCurrentRow(personDetail)
              setOpen('edit')
            }}
            className='h-8 gap-1 text-xs'
          >
            <Edit2 className='h-3.5 w-3.5' />
            <span>{t('actions.edit')}</span>
          </Button>
        </div>

        {/* 人脸底库主操作区与画廊 */}
        <div className='flex-1 space-y-5 overflow-y-auto px-6 py-5'>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <div>
              <h4 className='text-sm font-semibold text-foreground'>
                {t('sheet.faceGallery')} ({faceTotal})
              </h4>
              <p className='text-xs text-muted-foreground'>
                {t('fields.batchUploadTip')}
              </p>
            </div>

            <Button
              type='button'
              size='sm'
              disabled={isUploadingBatch}
              onClick={() => fileInputRef.current?.click()}
              className='h-8 gap-1.5 text-xs shadow-2xs'
            >
              {isUploadingBatch ? (
                <Loader2 className='h-3.5 w-3.5 animate-spin' />
              ) : (
                <Upload className='h-3.5 w-3.5' />
              )}
              <span>{t('fields.batchUpload')}</span>
            </Button>
          </div>

          <input
            type='file'
            ref={fileInputRef}
            multiple
            accept={FACE_IMAGE_ACCEPT}
            onChange={handleInputChange}
            className='hidden'
          />

          {batchProgress && (
            <div className='flex items-center gap-2 rounded-lg border border-primary/20 bg-primary/5 p-3 text-xs text-primary'>
              <Loader2 className='h-4 w-4 shrink-0 animate-spin' />
              <span>
                {t('fields.uploadingCount', {
                  current: batchProgress.current,
                  total: batchProgress.total,
                })}
              </span>
            </div>
          )}

          <button
            type='button'
            onClick={() => fileInputRef.current?.click()}
            onDragOver={(e) => {
              e.preventDefault()
              setIsDragOver(true)
            }}
            onDragLeave={() => setIsDragOver(false)}
            onDrop={handleDrop}
            className={cn(
              'group flex w-full cursor-pointer flex-col items-center justify-center rounded-xl border-2 border-dashed p-5 text-center transition-all select-none focus-visible:border-primary focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-hidden',
              isDragOver
                ? 'scale-99 border-primary bg-primary/10'
                : 'border-muted-foreground/25 hover:border-primary/50 hover:bg-muted/30'
            )}
          >
            <div className='mb-2 flex h-10 w-10 items-center justify-center rounded-full bg-muted transition-transform group-hover:scale-110'>
              <Plus className='h-5 w-5 text-muted-foreground group-hover:text-primary' />
            </div>
            <span className='text-xs font-medium text-foreground'>
              {t('fields.dragDropTip')}
            </span>
            <span className='mt-1 text-[11px] leading-tight text-muted-foreground/80'>
              {t('fields.dragDropDesc')}
            </span>
          </button>

          {isFacesError && (
            <div className='flex flex-col items-center justify-center gap-2 rounded-xl border border-border bg-card py-8 text-center text-destructive'>
              <ImageIcon className='h-6 w-6 opacity-40' />
              <span className='text-sm font-medium'>
                {t('table.loadFailed')}
              </span>
              <Button
                variant='outline'
                size='sm'
                onClick={() => void refetchFaces()}
                className='mt-1'
              >
                {t('actions.retry')}
              </Button>
            </div>
          )}

          <div className='grid grid-cols-2 gap-3.5 pb-6 sm:grid-cols-3'>
            {isLoadingFaces && (
              <>
                <Skeleton className='aspect-[4/5] rounded-xl' />
                <Skeleton className='aspect-[4/5] rounded-xl' />
                <Skeleton className='aspect-[4/5] rounded-xl' />
              </>
            )}

            {!isLoadingFaces &&
              !isFacesError &&
              faces.map((face) => (
                <FaceCard
                  key={face.id}
                  face={face}
                  personId={personId || ''}
                  isPrimary={face.id === effectivePrimaryFaceId}
                  isDeleting={deletingId === face.id}
                  isSettingPrimary={settingPrimaryId === face.id}
                  onDelete={(id) => void handleDeleteFace(id)}
                  onSetPrimary={(id) => void handleSetPrimary(id)}
                  onPreview={handlePreview}
                />
              ))}
          </div>

          {!isLoadingFaces && !isFacesError && faces.length === 0 && (
            <div className='flex flex-col items-center justify-center py-6 text-center'>
              <div className='mb-2 flex h-12 w-12 items-center justify-center rounded-full bg-muted text-muted-foreground'>
                <ImageIcon className='h-6 w-6' />
              </div>
              <h5 className='text-sm font-medium text-foreground'>
                {t('status.emptyFaces')}
              </h5>
              <p className='mt-1 max-w-xs text-xs text-muted-foreground'>
                {t('status.emptyFacesDesc')}
              </p>
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
