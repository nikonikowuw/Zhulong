import { type DragEvent, useRef, useState } from 'react'
import { RefreshCw, Trash2, UploadCloud } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'

interface ImageSearchDropzoneProps {
  selectedFile: File | null
  previewUrl: string | null
  onFileSelect: (file: File | null) => void
  disabled?: boolean
}

const MAX_FILE_SIZE = 10 * 1024 * 1024 // 10MB
const ALLOWED_MIME_TYPES = new Set(['image/jpeg', 'image/png', 'image/webp'])
const ALLOWED_EXTENSIONS = /\.(jpe?g|png|webp)$/i

function isAllowedImageFile(file: File): boolean {
  if (file.type && ALLOWED_MIME_TYPES.has(file.type)) {
    return true
  }
  return ALLOWED_EXTENSIONS.test(file.name)
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

export function ImageSearchDropzone({
  selectedFile,
  previewUrl,
  onFileSelect,
  disabled = false,
}: ImageSearchDropzoneProps) {
  const { t } = useTranslation('imageSearch')
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [isDragging, setIsDragging] = useState(false)
  const dragDepthRef = useRef(0)

  const handleValidateAndSelect = (file: File) => {
    if (!isAllowedImageFile(file)) {
      toast.error(t('dropzone.unsupportedFormat'))
      return
    }
    if (file.size > MAX_FILE_SIZE) {
      toast.error(t('dropzone.fileSizeExceeded'))
      return
    }
    onFileSelect(file)
  }

  const handleDragEnter = (e: DragEvent<HTMLDivElement>) => {
    e.preventDefault()
    e.stopPropagation()
    if (disabled) return
    dragDepthRef.current += 1
    setIsDragging(true)
  }

  const handleDragLeave = (e: DragEvent<HTMLDivElement>) => {
    e.preventDefault()
    e.stopPropagation()
    dragDepthRef.current -= 1
    if (dragDepthRef.current <= 0) {
      dragDepthRef.current = 0
      setIsDragging(false)
    }
  }

  const handleDragOver = (e: DragEvent<HTMLDivElement>) => {
    e.preventDefault()
    e.stopPropagation()
  }

  const handleDrop = (e: DragEvent<HTMLDivElement>) => {
    e.preventDefault()
    e.stopPropagation()
    dragDepthRef.current = 0
    setIsDragging(false)
    if (disabled) return
    const files = e.dataTransfer.files
    if (files && files.length > 0) {
      handleValidateAndSelect(files[0])
    }
  }

  const handleFileInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = e.target.files
    if (files && files.length > 0) {
      handleValidateAndSelect(files[0])
    }
    // Reset value so re-selecting the same file fires onChange
    e.target.value = ''
  }

  return (
    <Card className='relative overflow-hidden border-border/80 bg-card/60 p-4'>
      <input
        ref={fileInputRef}
        type='file'
        accept='.jpg,.jpeg,.png,.webp,image/jpeg,image/png,image/webp'
        className='hidden'
        disabled={disabled}
        onChange={handleFileInputChange}
      />

      {selectedFile && previewUrl ? (
        <div className='flex flex-col items-center gap-3'>
          <div className='relative max-h-72 w-full overflow-hidden rounded-lg border border-border bg-muted/30'>
            <img
              src={previewUrl}
              alt={t('dropzone.previewAlt')}
              className='mx-auto max-h-72 w-auto object-contain'
            />
          </div>

          <div className='flex w-full flex-wrap items-center justify-between gap-2 text-xs'>
            <span className='truncate font-medium text-foreground'>
              {t('dropzone.selected', {
                name: selectedFile.name,
                size: formatBytes(selectedFile.size),
              })}
            </span>

            <div className='flex items-center gap-1.5'>
              <Button
                type='button'
                variant='outline'
                size='sm'
                className='h-7 gap-1 text-xs'
                disabled={disabled}
                onClick={() => fileInputRef.current?.click()}
              >
                <RefreshCw className='h-3.5 w-3.5' />
                {t('dropzone.change')}
              </Button>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                className='h-7 gap-1 text-xs text-destructive hover:bg-destructive/10 hover:text-destructive'
                disabled={disabled}
                onClick={() => onFileSelect(null)}
              >
                <Trash2 className='h-3.5 w-3.5' />
                {t('dropzone.clear')}
              </Button>
            </div>
          </div>
        </div>
      ) : (
        <div
          onDragEnter={handleDragEnter}
          onDragLeave={handleDragLeave}
          onDragOver={handleDragOver}
          onDrop={handleDrop}
          onClick={() => !disabled && fileInputRef.current?.click()}
          className={`flex min-h-[220px] cursor-pointer flex-col items-center justify-center rounded-lg border-2 border-dashed p-6 text-center transition-colors ${
            disabled
              ? 'cursor-not-allowed opacity-50'
              : isDragging
                ? 'border-primary bg-primary/10'
                : 'border-muted-foreground/30 hover:border-primary/60 hover:bg-muted/20'
          }`}
          role='button'
          aria-label={t('dropzone.title')}
          aria-disabled={disabled}
          tabIndex={0}
          onKeyDown={(e) => {
            if (disabled) return
            if (e.key === 'Enter' || e.key === ' ') {
              e.preventDefault()
              fileInputRef.current?.click()
            }
          }}
        >
          <div className='mb-3 flex h-12 w-12 items-center justify-center rounded-full bg-primary/10 text-primary'>
            <UploadCloud className='h-6 w-6' />
          </div>
          <p className='text-sm font-medium text-foreground'>
            {t('dropzone.prompt')}
          </p>
          <p className='mt-1 max-w-md text-xs text-muted-foreground'>
            {t('dropzone.formatHint')}
          </p>
        </div>
      )}
    </Card>
  )
}
