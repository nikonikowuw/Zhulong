import { useRef, useState } from 'react'
import {
  Check,
  Copy,
  Edit2,
  Expand,
  Image as ImageIcon,
  Loader2,
  Plus,
  Star,
  Trash2,
  Upload,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
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
      className={cn(
        'group flex flex-col justify-between overflow-hidden rounded-xl border bg-card shadow-2xs transition-all duration-200 select-none hover:shadow-md',
        isPrimary
          ? 'border-teal-500/80 ring-2 ring-teal-500/20 dark:border-teal-400/80 dark:ring-teal-400/20'
          : 'border-border hover:border-primary/40'
      )}
    >
      {/* 照片展示区 */}
      <div
        onClick={() => !imgError && onPreview(imageUrl, displayName)}
        className='relative aspect-square w-full cursor-pointer overflow-hidden bg-muted/40'
      >
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
              className='gap-1 border-teal-300/80 bg-background/95 px-2 py-0.5 text-[10px] font-medium text-teal-800 shadow-xs backdrop-blur-xs dark:border-teal-700/80 dark:bg-background/90 dark:text-teal-200'
            >
              <Star className='h-3 w-3 fill-teal-600 text-teal-600 dark:fill-teal-400 dark:text-teal-400' />
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
            onClick={(e) => {
              e.stopPropagation()
              onPreview(imageUrl, displayName)
            }}
            className='h-7 w-7 rounded-full bg-background/85 shadow-xs backdrop-blur-xs hover:bg-background'
            title={t('imagePreview.enterFullscreen', {
              defaultValue: '查看大图',
            })}
          >
            <Expand className='h-3.5 w-3.5' />
          </Button>
        </div>
      </div>

      {/* 底部信息与常驻操作栏 */}
      <div className='flex flex-col gap-1.5 border-t border-border/50 bg-card p-2.5'>
        <div className='flex items-center justify-between text-[11px] text-muted-foreground'>
          {!imgError ? (
            <span className='truncate font-mono' title={displayName}>
              {displayName}
            </span>
          ) : (
            <span className='font-mono text-[10px]'>ID: {face.id}</span>
          )}
          <span className='shrink-0 text-[10px]'>
            {new Date(face.createdAt).toLocaleDateString()}
          </span>
        </div>

        <div className='flex items-center justify-between gap-1 pt-0.5'>
          {!isPrimary ? (
            <Button
              type='button'
              size='sm'
              variant='outline'
              disabled={isSettingPrimary || isDeleting}
              onClick={(e) => {
                e.stopPropagation()
                onSetPrimary(face.id)
              }}
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
            <div className='flex h-7 flex-1 items-center gap-1 text-[11px] font-medium text-teal-600 dark:text-teal-400'>
              <Check className='h-3.5 w-3.5' />
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
            onClick={(e) => {
              e.stopPropagation()
              onDelete(face.id)
            }}
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
}

export function PersonsFacesDialog() {
  const { t } = useTranslation('persons')
  const { open, setOpen, currentRow, setCurrentRow, setPreviewImage } =
    usePersonsContext()
  const [deletingId, setDeletingId] = useState<string | null>(null)
  const [settingPrimaryId, setSettingPrimaryId] = useState<string | null>(null)

  const [isDragOver, setIsDragOver] = useState(false)
  const [isUploadingBatch, setIsUploadingBatch] = useState(false)
  const [batchProgress, setBatchProgress] = useState<{
    current: number
    total: number
  } | null>(null)

  const fileInputRef = useRef<HTMLInputElement>(null)

  const isOpen = open === 'faces' && Boolean(currentRow)
  const personId = currentRow?.id

  const { data: facePage, isLoading: isLoadingFaces } = usePersonFaces(
    isOpen ? personId : undefined
  )
  const faces = facePage?.items ?? []
  const faceTotal = facePage?.total ?? 0

  const addFaceMutation = useAddFace({ suppressGlobalToast: true })
  const deleteFaceMutation = useDeleteFace()
  const setPrimaryMutation = useSetPrimaryFace()

  // 复制工号到剪贴板
  const handleCopyPersonId = () => {
    if (currentRow?.personId) {
      navigator.clipboard.writeText(currentRow.personId)
      toast.success(t('sheet.idCopied'))
    }
  }

  // 批量文件上传处理
  const handleFilesUpload = async (files: FileList | File[]) => {
    if (!personId || files.length === 0) return

    const validFiles: File[] = []
    for (let i = 0; i < files.length; i++) {
      const file = files[i]
      if (!ALLOWED_TYPES.includes(file.type)) {
        toast.error(`${file.name}: ${t('validation.invalidFileType')}`)
        continue
      }
      if (file.size > MAX_FILE_SIZE) {
        toast.error(`${file.name}: ${t('validation.fileTooLarge')}`)
        continue
      }
      validFiles.push(file)
    }

    if (validFiles.length === 0) return

    setIsUploadingBatch(true)
    setBatchProgress({ current: 0, total: validFiles.length })

    let successCount = 0
    for (let i = 0; i < validFiles.length; i++) {
      const file = validFiles[i]
      setBatchProgress({ current: i + 1, total: validFiles.length })
      try {
        const enrolled = await addFaceMutation.mutateAsync({
          personId,
          data: file,
        })
        if (faces.length === 0 && i === 0 && currentRow) {
          setCurrentRow({ ...currentRow, primaryFaceId: enrolled.id })
        }
        successCount++
      } catch (err: unknown) {
        const msg = err instanceof Error ? err.message : String(err)
        toast.error(`${file.name}: ${msg}`)
      }
    }

    setIsUploadingBatch(false)
    setBatchProgress(null)

    if (successCount > 0) {
      toast.success(t('notifications.addFaceSuccess'))
    }
  }

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files) {
      handleFilesUpload(e.target.files)
    }
    e.target.value = ''
  }

  const handleDrop = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault()
    setIsDragOver(false)
    if (e.dataTransfer.files) {
      handleFilesUpload(e.dataTransfer.files)
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
    } catch {
      // 错误由全局 mutation 处理
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
    } catch {
      // 错误由全局 mutation 处理
    } finally {
      setDeletingId(null)
    }
  }

  const handleClose = () => {
    if (isUploadingBatch || deletingId) return
    setOpen(null)
  }

  // 头像与首字母
  const primaryFaceId = currentRow?.primaryFaceId || faces[0]?.id
  const avatarUrl =
    personId && primaryFaceId
      ? personsApi.getFaceImageUrl(personId, primaryFaceId)
      : undefined
  const fallbackChar = currentRow?.name
    ? currentRow.name.trim().charAt(0).toUpperCase()
    : '?'

  return (
    <Sheet open={isOpen} onOpenChange={(v) => !v && handleClose()}>
      <SheetContent
        side='right'
        className='flex h-full w-full flex-col gap-0 p-0 sm:max-w-xl md:max-w-2xl'
      >
        {/* 顶部标题与描述（支持辅助技术与测试选择器） */}
        <SheetHeader className='border-b border-border px-6 py-4'>
          <div className='flex items-center justify-between'>
            <div className='space-y-1 pe-6'>
              <SheetTitle className='text-lg font-semibold tracking-tight'>
                {t('dialogs.facesTitle', { name: currentRow?.name || '' })}
              </SheetTitle>
              <SheetDescription className='text-xs text-muted-foreground'>
                {t('dialogs.facesDesc')}
              </SheetDescription>
            </div>
          </div>
        </SheetHeader>

        {/* 人员详细信息卡片 Bar */}
        <div className='flex items-center justify-between gap-4 border-b border-border/60 bg-muted/20 px-6 py-4'>
          <div className='flex items-center gap-3.5'>
            <Avatar className='h-12 w-12 rounded-full border-2 border-border shadow-xs'>
              {avatarUrl && (
                <AvatarImage
                  src={avatarUrl}
                  alt={currentRow?.name}
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
                  {currentRow?.name}
                </span>
                {currentRow?.personId ? (
                  <Badge
                    variant='secondary'
                    className='gap-1 font-mono text-[11px] text-muted-foreground'
                  >
                    <span>{currentRow.personId}</span>
                    <button
                      type='button'
                      onClick={handleCopyPersonId}
                      className='cursor-pointer hover:text-foreground'
                      title={t('sheet.copyId')}
                    >
                      <Copy className='h-3 w-3' />
                    </button>
                  </Badge>
                ) : (
                  <Badge
                    variant='outline'
                    className='text-[10px] text-muted-foreground'
                  >
                    {t('status.unregistered')}
                  </Badge>
                )}
              </div>
              <p className='text-xs text-muted-foreground'>
                {t('fields.createdAt')}:{' '}
                {currentRow?.createdAt
                  ? new Date(currentRow.createdAt).toLocaleString()
                  : '-'}
              </p>
            </div>
          </div>

          <div className='flex items-center gap-2'>
            <Button
              type='button'
              size='sm'
              variant='outline'
              onClick={() => {
                setOpen('edit')
              }}
              className='h-8 gap-1 text-xs'
            >
              <Edit2 className='h-3.5 w-3.5' />
              <span>{t('actions.edit')}</span>
            </Button>
          </div>
        </div>

        {/* 人脸底库主操作区与画廊 */}
        <div className='flex-1 space-y-5 overflow-y-auto px-6 py-5'>
          {/* 标题栏与批量上传按钮 */}
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
            accept='image/jpeg,image/png,image/webp'
            onChange={handleInputChange}
            className='hidden'
          />

          {/* 批量上传状态条 */}
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

          {/* 常驻拖拽上传区域 */}
          <div
            onClick={() => fileInputRef.current?.click()}
            onDragOver={(e) => {
              e.preventDefault()
              setIsDragOver(true)
            }}
            onDragLeave={() => setIsDragOver(false)}
            onDrop={handleDrop}
            className={cn(
              'group flex cursor-pointer flex-col items-center justify-center rounded-xl border-2 border-dashed p-5 text-center transition-all select-none',
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
          </div>

          {/* 画廊网格 */}
          <div className='grid grid-cols-2 gap-3.5 pb-6 sm:grid-cols-3'>
            {/* 骨架屏加载态 */}
            {isLoadingFaces && (
              <>
                <Skeleton className='aspect-[4/5] rounded-xl' />
                <Skeleton className='aspect-[4/5] rounded-xl' />
                <Skeleton className='aspect-[4/5] rounded-xl' />
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
                    onPreview={(url, title) => setPreviewImage({ url, title })}
                  />
                )
              })}
          </div>

          {/* 无人脸时的空状态引导 */}
          {!isLoadingFaces && faces.length === 0 && (
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
