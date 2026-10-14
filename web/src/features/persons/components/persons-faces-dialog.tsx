import { useEffect, useRef, useState } from 'react'
import {
  Check,
  Image as ImageIcon,
  Loader2,
  Plus,
  Star,
  Trash2,
  X,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { personsApi } from '../api/persons-api'
import { type Face } from '../data/schema'
import {
  useAddFace,
  useDeleteFace,
  usePersonFaces,
  useSetPrimaryFace,
} from '../hooks/use-persons'
import { usePersonsContext } from './persons-provider'

const MAX_FILE_SIZE = 10 * 1024 * 1024 // 10MB
const ALLOWED_TYPES = ['image/jpeg', 'image/png', 'image/webp']

interface FaceCardProps {
  face: Face
  personId: string
  isPrimary: boolean
  isDeleting: boolean
  isSettingPrimary: boolean
  onDelete: (faceId: string) => void
  onSetPrimary: (faceId: string) => void
  onPreview: (imageUrl: string, title: string) => void
}

function FaceCard({
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
  const imageUrl = personsApi.getFaceImageUrl(personId, face.id)

  const displayName = face.imagePath
    ? face.imagePath.split('/').pop() || face.imagePath
    : face.id

  return (
    <div
      data-testid='face-card'
      title={displayName}
      onClick={() => !imgError && onPreview(imageUrl, displayName)}
      className={cn(
        'group relative aspect-square overflow-hidden rounded-xl border bg-muted/30 transition-all duration-200 select-none cursor-pointer',
        isPrimary
          ? 'border-teal-500 shadow-sm ring-2 ring-teal-500/25 dark:border-teal-400 dark:ring-teal-400/25'
          : 'border-border hover:border-primary/40 hover:shadow-sm'
      )}
    >
      {/* 照片主体 */}
      {!imgError ? (
        <img
          src={imageUrl}
          alt={displayName}
          onError={() => setImgError(true)}
          className='h-full w-full object-cover transition-transform duration-300 group-hover:scale-105'
        />
      ) : (
        <div className='flex h-full w-full flex-col items-center justify-center gap-1.5 p-2 text-center text-muted-foreground'>
          <ImageIcon className='h-6 w-6 text-muted-foreground/50' />
          <span className='font-mono text-[10px] text-muted-foreground/70 line-clamp-1'>
            {displayName}
          </span>
        </div>
      )}

      {/* 主人脸角标 */}
      {isPrimary && (
        <div className='absolute start-2 top-2 z-10 pointer-events-none'>
          <Badge
            variant='outline'
            className='gap-1 border-teal-200/90 bg-background/90 px-1.5 py-0.5 text-[10px] font-medium text-teal-800 shadow-xs backdrop-blur-xs dark:border-teal-800/90 dark:bg-background/90 dark:text-teal-200'
          >
            <Star className='h-2.5 w-2.5 fill-teal-600 text-teal-600 dark:fill-teal-400 dark:text-teal-400' />
            <span>{t('fields.primaryFace')}</span>
          </Badge>
        </div>
      )}

      {/* 右上角删除操作按钮 */}
      <Button
        variant='ghost'
        size='sm'
        disabled={isDeleting || isSettingPrimary}
        onClick={(e) => {
          e.stopPropagation()
          onDelete(face.id)
        }}
        className='absolute end-1.5 top-1.5 z-10 h-7 w-7 rounded-full bg-background/80 p-0 text-muted-foreground opacity-0 shadow-xs backdrop-blur-xs transition-opacity hover:bg-destructive hover:text-destructive-foreground group-hover:opacity-100 focus:opacity-100'
        aria-label={t('actions.deleteFace')}
        title={t('actions.deleteFace')}
      >
        {isDeleting ? (
          <Loader2 className='h-3.5 w-3.5 animate-spin' />
        ) : (
          <Trash2 className='h-3.5 w-3.5' />
        )}
      </Button>

      {/* 底部悬浮信息与设为主图操作蒙层 */}
      <div className='absolute inset-x-0 bottom-0 z-10 flex flex-col justify-end bg-gradient-to-t from-black/80 via-black/40 to-transparent p-2 pt-6 opacity-0 transition-opacity duration-200 group-hover:opacity-100 focus-within:opacity-100'>
        {!isPrimary ? (
          <Button
            size='sm'
            variant='secondary'
            disabled={isSettingPrimary || isDeleting}
            onClick={(e) => {
              e.stopPropagation()
              onSetPrimary(face.id)
            }}
            className='h-7 w-full gap-1 rounded-md bg-background/90 text-xs font-normal shadow-xs backdrop-blur-xs hover:bg-background'
            title={t('actions.setPrimary')}
          >
            {isSettingPrimary ? (
              <Loader2 className='h-3 w-3 animate-spin' />
            ) : (
              <Star className='h-3 w-3' />
            )}
            <span>{t('actions.setPrimary')}</span>
          </Button>
        ) : (
          <p
            className='truncate text-center font-mono text-[10px] text-white/90 drop-shadow-xs'
            title={displayName}
          >
            {new Date(face.createdAt).toLocaleDateString()}
          </p>
        )}
      </div>
    </div>
  )
}

export function PersonsFacesDialog() {
  const { t } = useTranslation('persons')
  const { open, setOpen, currentRow, setCurrentRow, setPreviewImage } =
    usePersonsContext()
  const [deletingId, setDeletingId] = useState<string | null>(null)
  const [settingPrimaryId, setSettingPrimaryId] = useState<string | null>(null)

  const [selectedFile, setSelectedFile] = useState<File | null>(null)
  const [isDragOver, setIsDragOver] = useState(false)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const isOpen = open === 'faces' && Boolean(currentRow)
  const personId = currentRow?.id

  const { data: faces = [], isLoading: isLoadingFaces } = usePersonFaces(
    isOpen ? personId : undefined
  )

  const addFaceMutation = useAddFace()
  const deleteFaceMutation = useDeleteFace()
  const setPrimaryMutation = useSetPrimaryFace()

  // 选中的本地图片预览 URL
  const [previewUrl, setPreviewUrl] = useState<string | null>(null)

  const clearSelection = () => {
    setSelectedFile(null)
    setPreviewUrl((prev) => {
      if (prev) URL.revokeObjectURL(prev)
      return null
    })
  }

  useEffect(() => {
    return () => {
      if (previewUrl) {
        URL.revokeObjectURL(previewUrl)
      }
    }
  }, [previewUrl])

  const handleFileSelect = (file: File) => {
    if (!ALLOWED_TYPES.includes(file.type)) {
      toast.error(t('validation.invalidFileType'))
      return
    }
    if (file.size > MAX_FILE_SIZE) {
      toast.error(t('validation.fileTooLarge'))
      return
    }
    setSelectedFile(file)
    setPreviewUrl((prev) => {
      if (prev) URL.revokeObjectURL(prev)
      return URL.createObjectURL(file)
    })
  }

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (file) {
      handleFileSelect(file)
    }
    e.target.value = ''
  }

  const handleDrop = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault()
    setIsDragOver(false)
    const file = e.dataTransfer.files?.[0]
    if (file) {
      handleFileSelect(file)
    }
  }

  const handleUpload = async () => {
    if (!personId || !selectedFile) return
    try {
      const enrolled = await addFaceMutation.mutateAsync({
        personId,
        data: selectedFile,
      })
      if (faces.length === 0 && currentRow) {
        setCurrentRow({ ...currentRow, primaryFaceId: enrolled.id })
      }
      toast.success(t('notifications.addFaceSuccess'))
      clearSelection()
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      toast.error(msg)
    }
  }

  const handleSetPrimary = async (faceId: string) => {
    if (!personId) return
    setSettingPrimaryId(faceId)
    try {
      await setPrimaryMutation.mutateAsync({
        personId,
        faceId,
      })
      if (currentRow) {
        setCurrentRow({ ...currentRow, primaryFaceId: faceId })
      }
      toast.success(t('notifications.setPrimarySuccess'))
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      toast.error(msg)
    } finally {
      setSettingPrimaryId(null)
    }
  }

  const handleDeleteFace = async (faceId: string) => {
    if (!personId) return
    setDeletingId(faceId)
    try {
      await deleteFaceMutation.mutateAsync({
        personId,
        faceId,
      })
      toast.success(t('notifications.deleteFaceSuccess'))
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      toast.error(msg)
    } finally {
      setDeletingId(null)
    }
  }

  const handleClose = () => {
    if (addFaceMutation.isPending || deletingId) return
    setOpen(null)
    clearSelection()
  }

  return (
    <>
      <Dialog open={isOpen} onOpenChange={(v) => !v && handleClose()}>
        <DialogContent className='overflow-hidden sm:max-w-xl'>
          <DialogHeader>
            <DialogTitle>
              {t('dialogs.facesTitle', { name: currentRow?.name || '' })}
            </DialogTitle>
            <DialogDescription>{t('dialogs.facesDesc')}</DialogDescription>
          </DialogHeader>

          <div className='space-y-4 py-2'>
            {/* 画廊相册网格 (Gallery Grid) */}
            <div className='flex items-center justify-between'>
              <h4 className='text-xs font-semibold uppercase tracking-wider text-muted-foreground'>
                {t('table.columns.faceCount')} ({faces.length})
              </h4>
              <span className='text-[11px] text-muted-foreground/80'>
                {t('fields.dragDropDesc')}
              </span>
            </div>

            <input
              type='file'
              ref={fileInputRef}
              accept='image/jpeg,image/png,image/webp'
              onChange={handleInputChange}
              className='hidden'
            />

            <div className='max-h-[56vh] w-full overflow-y-auto pe-1'>
              <div className='grid grid-cols-2 sm:grid-cols-3 gap-3.5'>
                {/* 骨架屏加载态 */}
                {isLoadingFaces && (
                  <>
                    <Skeleton className='aspect-square rounded-xl' />
                    <Skeleton className='aspect-square rounded-xl' />
                    <Skeleton className='aspect-square rounded-xl' />
                  </>
                )}

                {/* 已录入底库卡片 */}
                {!isLoadingFaces &&
                  faces.map((face) => {
                    const isPrimary = currentRow?.primaryFaceId
                      ? currentRow.primaryFaceId === face.id
                      : faces[0]?.id === face.id

                    return (
                      <FaceCard
                        key={face.id}
                        face={face}
                        personId={personId || ''}
                        isPrimary={isPrimary}
                        isDeleting={deletingId === face.id}
                        isSettingPrimary={settingPrimaryId === face.id}
                        onDelete={handleDeleteFace}
                        onSetPrimary={handleSetPrimary}
                        onPreview={(url, title) =>
                          setPreviewImage({ url, title })
                        }
                      />
                    )
                  })}

                {/* 上传新人脸卡片（如果当前选了文件则显示确认卡片，未选则显示 + 虚线按钮） */}
                {!selectedFile ? (
                  <div
                    onClick={() => fileInputRef.current?.click()}
                    onDragOver={(e) => {
                      e.preventDefault()
                      setIsDragOver(true)
                    }}
                    onDragLeave={() => setIsDragOver(false)}
                    onDrop={handleDrop}
                    className={cn(
                      'group relative flex aspect-square cursor-pointer flex-col items-center justify-center rounded-xl border-2 border-dashed p-3 text-center transition-all duration-200 select-none',
                      isDragOver
                        ? 'border-primary bg-primary/10 scale-98'
                        : 'border-muted-foreground/25 hover:border-primary/50 hover:bg-muted/40'
                    )}
                  >
                    <div className='mb-2 flex h-9 w-9 items-center justify-center rounded-full bg-muted transition-transform group-hover:scale-110'>
                      <Plus className='h-5 w-5 text-muted-foreground group-hover:text-primary' />
                    </div>
                    <span className='text-xs font-medium text-foreground'>
                      {t('dialogs.addFaceTitle')}
                    </span>
                    <span className='mt-1 text-[11px] text-muted-foreground/75 leading-tight'>
                      {t('fields.dragDropTip')}
                    </span>
                  </div>
                ) : (
                  /* 选中待上传预览卡片 */
                  <div className='relative aspect-square overflow-hidden rounded-xl border-2 border-primary shadow-sm bg-muted'>
                    {previewUrl && (
                      <img
                        src={previewUrl}
                        alt='preview'
                        className='h-full w-full object-cover'
                      />
                    )}
                    {/* 上传操作蒙层 */}
                    <div className='absolute inset-0 flex flex-col justify-between bg-black/50 p-2'>
                      <div className='flex justify-end'>
                        <Button
                          variant='ghost'
                          size='sm'
                          disabled={addFaceMutation.isPending}
                          onClick={clearSelection}
                          className='h-6 w-6 rounded-full bg-background/80 p-0 text-foreground hover:bg-destructive hover:text-destructive-foreground'
                          aria-label={t('fields.removeSelected')}
                        >
                          <X className='h-3.5 w-3.5' />
                        </Button>
                      </div>

                      <div className='space-y-1.5'>
                        <p className='truncate text-center font-mono text-[10px] text-white'>
                          {selectedFile.name}
                        </p>
                        <Button
                          size='sm'
                          disabled={addFaceMutation.isPending}
                          onClick={handleUpload}
                          className='h-7 w-full gap-1 text-xs'
                        >
                          {addFaceMutation.isPending ? (
                            <Loader2 className='h-3.5 w-3.5 animate-spin' />
                          ) : (
                            <Check className='h-3.5 w-3.5' />
                          )}
                          <span>{t('fields.uploadAndEnroll')}</span>
                        </Button>
                      </div>
                    </div>
                  </div>
                )}
              </div>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </>
  )
}
