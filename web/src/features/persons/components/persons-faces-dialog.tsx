import { useEffect, useRef, useState } from 'react'
import {
  CheckCircle2,
  Image as ImageIcon,
  Loader2,
  Trash2,
  UploadCloud,
  X,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
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
import { useAddFace, useDeleteFace, usePersonFaces } from '../hooks/use-persons'
import { usePersonsContext } from './persons-provider'

const MAX_FILE_SIZE = 10 * 1024 * 1024 // 10MB
const ALLOWED_TYPES = ['image/jpeg', 'image/png', 'image/webp']

function FaceItem({
  face,
  personId,
  isDeleting,
  onDelete,
}: {
  face: Face
  personId: string
  isDeleting: boolean
  onDelete: (faceId: string) => void
}) {
  const { t } = useTranslation('persons')
  const [imgError, setImgError] = useState(false)
  const imageUrl = personsApi.getFaceImageUrl(personId, face.id)

  const displayName = face.imagePath
    ? face.imagePath.split('/').pop() || face.imagePath
    : face.id

  return (
    <div className='flex items-center justify-between rounded-md border bg-card p-2.5 shadow-xs transition-colors hover:border-primary/30'>
      <div className='flex items-center gap-3 overflow-hidden'>
        <div className='relative flex h-11 w-11 shrink-0 items-center justify-center overflow-hidden rounded-md border bg-muted'>
          {!imgError ? (
            <img
              src={imageUrl}
              alt='face'
              onError={() => setImgError(true)}
              className='h-full w-full object-cover'
            />
          ) : (
            <ImageIcon className='h-5 w-5 text-muted-foreground/60' />
          )}
        </div>
        <div className='min-w-0 flex-1'>
          <p
            className='truncate font-mono text-xs font-medium text-foreground'
            title={face.imagePath || face.id}
          >
            {displayName}
          </p>
          <p className='text-[11px] text-muted-foreground'>
            {new Date(face.createdAt).toLocaleString()}
          </p>
        </div>
      </div>
      <Button
        variant='ghost'
        size='sm'
        disabled={isDeleting}
        onClick={() => onDelete(face.id)}
        className='h-8 w-8 p-0 text-muted-foreground hover:text-destructive'
        aria-label={t('actions.deleteFace')}
      >
        {isDeleting ? (
          <Loader2 className='h-4 w-4 animate-spin' />
        ) : (
          <Trash2 className='h-4 w-4' />
        )}
      </Button>
    </div>
  )
}

export function PersonsFacesDialog() {
  const { t } = useTranslation('persons')
  const { open, setOpen, currentRow } = usePersonsContext()
  const [deletingId, setDeletingId] = useState<string | null>(null)

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

  // 渲染派生预览 URL，并在卸载/切换时释放
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
    // 重置 input 以便能重复选择同一文件
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
      await addFaceMutation.mutateAsync({
        personId,
        data: selectedFile,
      })
      toast.success(t('notifications.addFaceSuccess'))
      clearSelection()
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      toast.error(msg)
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
    <Dialog open={isOpen} onOpenChange={(v) => !v && handleClose()}>
      <DialogContent className='sm:max-w-xl'>
        <DialogHeader>
          <DialogTitle>
            {t('dialogs.facesTitle', { name: currentRow?.name || '' })}
          </DialogTitle>
          <DialogDescription>{t('dialogs.facesDesc')}</DialogDescription>
        </DialogHeader>

        <div className='space-y-5 py-2'>
          {/* 已录入的人脸底库列表 */}
          <div className='space-y-2.5'>
            <div className='flex items-center justify-between'>
              <h4 className='text-xs font-semibold uppercase tracking-wider text-muted-foreground'>
                {t('table.columns.faceCount')} ({faces.length})
              </h4>
            </div>

            {isLoadingFaces ? (
              <div className='space-y-2'>
                <Skeleton className='h-12 w-full' />
                <Skeleton className='h-12 w-full' />
              </div>
            ) : faces.length === 0 ? (
              <div className='flex flex-col items-center justify-center rounded-lg border border-dashed p-6 text-center text-muted-foreground'>
                <ImageIcon className='mb-2 h-8 w-8 text-muted-foreground/60' />
                <p className='text-sm font-medium'>{t('status.emptyFaces')}</p>
                <p className='text-xs text-muted-foreground/80'>
                  {t('status.emptyFacesDesc')}
                </p>
              </div>
            ) : (
              <div className='max-h-52 space-y-2 overflow-y-auto pe-1'>
                {faces.map((face) => (
                  <FaceItem
                    key={face.id}
                    face={face}
                    personId={personId || ''}
                    isDeleting={deletingId === face.id}
                    onDelete={handleDeleteFace}
                  />
                ))}
              </div>
            )}
          </div>

          {/* 录入新人脸卡片（图片文件选择与拖拽） */}
          <div className='rounded-lg border bg-muted/30 p-4'>
            <div className='mb-3'>
              <h4 className='flex items-center gap-1.5 text-sm font-medium text-foreground'>
                <UploadCloud className='h-4 w-4 text-primary' />
                {t('dialogs.addFaceTitle')}
              </h4>
              <p className='text-xs text-muted-foreground'>
                {t('dialogs.addFaceDesc')}
              </p>
            </div>

            <input
              type='file'
              ref={fileInputRef}
              accept='image/jpeg,image/png,image/webp'
              onChange={handleInputChange}
              className='hidden'
            />

            {!selectedFile ? (
              <div
                onClick={() => fileInputRef.current?.click()}
                onDragOver={(e) => {
                  e.preventDefault()
                  setIsDragOver(true)
                }}
                onDragLeave={() => setIsDragOver(false)}
                onDrop={handleDrop}
                className={`flex cursor-pointer flex-col items-center justify-center rounded-md border-2 border-dashed p-5 text-center transition-colors ${
                  isDragOver
                    ? 'border-primary bg-primary/5'
                    : 'border-muted-foreground/25 hover:border-primary/50 hover:bg-muted/50'
                }`}
              >
                <UploadCloud className='mb-2 h-7 w-7 text-muted-foreground' />
                <p className='text-xs font-medium text-foreground'>
                  {t('fields.dragDropTip')}
                </p>
                <p className='mt-0.5 text-[11px] text-muted-foreground'>
                  {t('fields.dragDropDesc')}
                </p>
              </div>
            ) : (
              <div className='space-y-3'>
                <div className='flex items-center justify-between rounded-md border bg-card p-2.5'>
                  <div className='flex items-center gap-3 overflow-hidden'>
                    {previewUrl ? (
                      <img
                        src={previewUrl}
                        alt='preview'
                        className='h-12 w-12 shrink-0 rounded-md object-cover border'
                      />
                    ) : (
                      <div className='flex h-12 w-12 shrink-0 items-center justify-center rounded-md bg-muted'>
                        <ImageIcon className='h-6 w-6 text-muted-foreground' />
                      </div>
                    )}
                    <div className='min-w-0 flex-1'>
                      <p className='truncate text-xs font-medium text-foreground'>
                        {selectedFile.name}
                      </p>
                      <p className='text-[11px] text-muted-foreground'>
                        {(selectedFile.size / (1024 * 1024)).toFixed(2)} MB
                      </p>
                      <span className='inline-flex items-center gap-1 text-[11px] text-emerald-600 dark:text-emerald-400'>
                        <CheckCircle2 className='h-3 w-3' />
                        {t('fields.dragDropDesc')}
                      </span>
                    </div>
                  </div>
                  <Button
                    variant='ghost'
                    size='sm'
                    onClick={clearSelection}
                    className='h-8 w-8 p-0 text-muted-foreground hover:text-destructive'
                    aria-label={t('fields.removeSelected')}
                  >
                    <X className='h-4 w-4' />
                  </Button>
                </div>

                <div className='flex justify-end gap-2'>
                  <Button
                    variant='outline'
                    size='sm'
                    onClick={() => fileInputRef.current?.click()}
                    disabled={addFaceMutation.isPending}
                  >
                    {t('fields.changePhoto')}
                  </Button>
                  <Button
                    size='sm'
                    onClick={handleUpload}
                    disabled={addFaceMutation.isPending}
                  >
                    {addFaceMutation.isPending && (
                      <Loader2 className='me-1.5 h-3.5 w-3.5 animate-spin' />
                    )}
                    {t('fields.uploadAndEnroll')}
                  </Button>
                </div>
              </div>
            )}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
