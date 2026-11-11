import { useCallback, useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
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
import {
  PERSONS_QUERY_KEY,
  personFacesQueryKey,
  useAddFace,
  useDeleteFace,
  usePersonFaces,
  useSetPrimaryFace,
} from '../hooks/use-persons'
import {
  FACE_IMAGE_ACCEPT,
  initialOf,
  primaryFaceIdOf,
  validateFaceImageFile,
} from '../persons-helpers'
import { FaceCard } from './persons-face-card'
import { usePersonsContext } from './persons-provider'

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

  const {
    data: facePage,
    isLoading: isLoadingFaces,
    isError: isFacesError,
    refetch: refetchFaces,
  } = usePersonFaces(isOpen ? personId : undefined)
  const faces = facePage?.items ?? []
  const faceTotal = facePage?.total ?? 0

  const queryClient = useQueryClient()
  const addFaceBatchMutation = useAddFace({
    suppressGlobalToast: true,
    suppressInvalidate: true,
  })
  // 单张删除/设主脸自带 toast 与错误横幅，交给全局 MutationCache 会重复弹窗。
  const deleteFaceMutation = useDeleteFace({ suppressGlobalToast: true })
  const setPrimaryMutation = useSetPrimaryFace({ suppressGlobalToast: true })

  // 复制工号到剪贴板。
  //
  // 不能直接 `navigator.clipboard.writeText`：边缘设备以 `http://192.168.x.x:8000`
  // 暴露服务时它不是安全上下文，`navigator.clipboard` 是 `undefined`，裸调会在
  // 同步求值阶段抛 TypeError；`copyToClipboard` 带 execCommand 降级。
  const handleCopyPersonId = async () => {
    const value = currentRow?.personId
    if (!value) return
    const ok = await copyToClipboard(value)
    if (ok) {
      toast.success(t('sheet.idCopied'))
    } else {
      toast.error(t('sheet.idCopyFailed'))
    }
  }

  // 批量文件上传处理（整批完成统一刷新 Query，避免 Invalidation 洪水）
  //
  // 依赖里**不能**出现 `addFaceBatchMutation` / `faces.length` / `currentRow`：
  // mutation 结果对象每次渲染都新建，`faces`/`currentRow` 也会随上传进度
  // 变化，把它们放进依赖会让 `useCallback` 完全失效（批量上传时 `batchProgress`
  // 每次 setState 都重建一次）。可变信息一律走 ref，稳定引用才进依赖。
  const uploadRef = useRef({
    mutate: addFaceBatchMutation.mutateAsync,
    hasExistingFaces: faces.length > 0,
    currentRow,
  })
  useEffect(() => {
    uploadRef.current = {
      mutate: addFaceBatchMutation.mutateAsync,
      hasExistingFaces: faces.length > 0,
      currentRow,
    }
  }, [addFaceBatchMutation.mutateAsync, faces.length, currentRow])

  const handleFilesUpload = useCallback(
    async (files: FileList | File[]) => {
      if (!personId || files.length === 0) return

      const validFiles: File[] = []
      for (let i = 0; i < files.length; i++) {
        const file = files[i]
        const problem = validateFaceImageFile(file)
        if (problem) {
          toast.error(
            `${file.name}: ${
              problem === 'invalidType'
                ? t('validation.invalidFileType')
                : t('validation.fileTooLarge')
            }`
          )
          continue
        }
        validFiles.push(file)
      }

      if (validFiles.length === 0) return

      setIsUploadingBatch(true)
      setBatchProgress({ current: 0, total: validFiles.length })

      const { mutate, hasExistingFaces, currentRow: row } = uploadRef.current
      let successCount = 0
      for (let i = 0; i < validFiles.length; i++) {
        const file = validFiles[i]
        setBatchProgress({ current: i + 1, total: validFiles.length })
        try {
          const enrolled = await mutate({ personId, data: file })
          // 首张入库时把它设为主人脸，避免抽屉顶部继续显示占位头像。
          if (!hasExistingFaces && i === 0 && row) {
            setCurrentRow({ ...row, primaryFaceId: enrolled.id })
          }
          successCount++
        } catch (err: unknown) {
          // 每张失败单独报出文件名与原因；错误文案走统一本地化。
          toast.error(`${file.name}: ${errorMessageOf(err)}`)
        }
      }

      setIsUploadingBatch(false)
      setBatchProgress(null)

      if (successCount > 0) {
        void queryClient.invalidateQueries({
          queryKey: personFacesQueryKey(personId),
        })
        void queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })
        toast.success(
          successCount === validFiles.length
            ? t('notifications.addFaceSuccess')
            : t('notifications.addFacePartial', {
                success: successCount,
                failed: validFiles.length - successCount,
              })
        )
      }
    },
    [personId, t, setCurrentRow, queryClient]
  )

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files) {
      void handleFilesUpload(e.target.files)
    }
    e.target.value = ''
  }

  const handleDrop = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault()
    setIsDragOver(false)
    if (e.dataTransfer.files) {
      void handleFilesUpload(e.dataTransfer.files)
    }
  }

  const handleSetPrimary = async (faceId: string) => {
    if (!personId) return
    setSettingPrimaryId(faceId)
    try {
      await setPrimaryMutation.mutateAsync({ personId, faceId })
      if (currentRow) {
        setCurrentRow({ ...currentRow, primaryFaceId: faceId })
      }
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

  // 头像与首字母
  const primaryFaceId = currentRow
    ? primaryFaceIdOf({ ...currentRow, faces })
    : undefined
  const avatarUrl = getGalleryImageUrl(personId, primaryFaceId)
  const fallbackChar = initialOf(currentRow?.name)

  return (
    <Sheet open={isOpen} onOpenChange={(v) => !v && handleClose()}>
      <SheetContent
        side='right'
        className='flex h-full w-full flex-col gap-0 p-0 sm:max-w-xl md:max-w-2xl'
      >
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
                    {t('status.unregistered')}
                  </Badge>
                )}
              </div>
              <p className='text-xs text-muted-foreground'>
                {t('fields.createdAt')}:{' '}
                {currentRow?.createdAt
                  ? formatLocalDateTime(currentRow.createdAt)
                  : '-'}
              </p>
            </div>
          </div>

          <div className='flex items-center gap-2'>
            <Button
              type='button'
              size='sm'
              variant='outline'
              onClick={() => setOpen('edit')}
              className='h-8 gap-1 text-xs'
            >
              <Edit2 className='h-3.5 w-3.5' />
              <span>{t('actions.edit')}</span>
            </Button>
          </div>
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

          {/* 加载失败：不能落进"暂无人脸底库照片"的空态，否则用户会以为
              数据丢了并开始重复上传。 */}
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

          {/* 画廊网格 */}
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
                    onDelete={(id) => void handleDeleteFace(id)}
                    onSetPrimary={(id) => void handleSetPrimary(id)}
                    onPreview={handlePreview}
                  />
                )
              })}
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
