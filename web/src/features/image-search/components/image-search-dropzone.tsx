import { type DragEvent, useRef, useState } from 'react'
import {
  CheckCircle2,
  FileImage,
  RefreshCw,
  Scan,
  Sparkles,
  Trash2,
  UploadCloud,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Badge } from '@/components/ui/badge'
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
    <Card className='group relative overflow-hidden border-border/70 bg-card/60 p-4 shadow-xs backdrop-blur-xs transition-all duration-200 hover:border-primary/40 hover:shadow-md'>
      <input
        ref={fileInputRef}
        type='file'
        accept='.jpg,.jpeg,.png,.webp,image/jpeg,image/png,image/webp'
        className='hidden'
        disabled={disabled}
        onChange={handleFileInputChange}
      />

      {selectedFile && previewUrl ? (
        <div className='flex flex-col gap-3.5'>
          {/* 上传人脸预览框，附带 HUD 瞄准四角括号与微光扫描感 */}
          <div className='relative mx-auto flex max-h-72 w-full items-center justify-center overflow-hidden rounded-xl border border-primary/20 bg-muted/40 p-2'>
            {/* 背景科技微网格 */}
            <div className='pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_center,color-mix(in_oklch,var(--primary)_6%,transparent)_0,transparent_70%)]' />

            {/* HUD 瞄准角标 */}
            <div className='pointer-events-none absolute inset-2.5 z-10'>
              <div className='absolute top-0 left-0 h-3 w-3 border-t-2 border-l-2 border-primary/70' />
              <div className='absolute top-0 right-0 h-3 w-3 border-t-2 border-r-2 border-primary/70' />
              <div className='absolute bottom-0 left-0 h-3 w-3 border-b-2 border-l-2 border-primary/70' />
              <div className='absolute right-0 bottom-0 h-3 w-3 border-r-2 border-b-2 border-primary/70' />
            </div>

            <img
              src={previewUrl}
              alt={t('dropzone.previewAlt')}
              className='relative z-0 mx-auto max-h-64 w-auto rounded-lg object-contain shadow-sm'
            />

            {/* 瞄准中提示徽章 */}
            <div className='pointer-events-none absolute start-4 bottom-4 z-10 flex items-center gap-1.5 rounded-full border border-primary/30 bg-background/85 px-2.5 py-0.5 text-[11px] font-medium text-foreground backdrop-blur-sm'>
              <span className='h-1.5 w-1.5 animate-pulse rounded-full bg-emerald-500' />
              <Scan className='h-3 w-3 text-primary' />
              <span>{t('dropzone.tagFaceReq')}</span>
            </div>
          </div>

          {/* 图像文件元信息与快捷操作 */}
          <div className='flex flex-col gap-2 rounded-lg border border-border/60 bg-muted/30 p-2.5 sm:flex-row sm:items-center sm:justify-between'>
            <div className='flex min-w-0 items-center gap-2'>
              <div className='flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary'>
                <FileImage className='h-4 w-4' />
              </div>
              <div className='min-w-0 flex-1'>
                <p className='truncate text-xs font-semibold text-foreground'>
                  {t('dropzone.selected', {
                    name: selectedFile.name,
                    size: formatBytes(selectedFile.size),
                  })}
                </p>
                <div className='flex items-center gap-1.5 text-[10px] text-muted-foreground'>
                  <CheckCircle2 className='h-3 w-3 text-emerald-500' />
                  <span>{formatBytes(selectedFile.size)}</span>
                  <span>•</span>
                  <span className='uppercase'>
                    {selectedFile.type.replace('image/', '') || 'IMAGE'}
                  </span>
                </div>
              </div>
            </div>

            <div className='flex items-center gap-1.5 self-end sm:self-center'>
              <Button
                type='button'
                variant='outline'
                size='sm'
                className='h-7.5 gap-1.5 px-2.5 text-xs transition-colors hover:border-primary/50 hover:bg-primary/5'
                disabled={disabled}
                onClick={() => fileInputRef.current?.click()}
              >
                <RefreshCw className='h-3.5 w-3.5 text-muted-foreground' />
                <span>{t('dropzone.change')}</span>
              </Button>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                className='h-7.5 gap-1.5 px-2.5 text-xs text-destructive hover:bg-destructive/10 hover:text-destructive'
                disabled={disabled}
                onClick={() => onFileSelect(null)}
              >
                <Trash2 className='h-3.5 w-3.5' />
                <span>{t('dropzone.clear')}</span>
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
          className={`relative flex min-h-[240px] cursor-pointer flex-col items-center justify-center overflow-hidden rounded-xl border-2 border-dashed p-6 text-center transition-all duration-200 ${
            disabled
              ? 'cursor-not-allowed opacity-50'
              : isDragging
                ? 'border-primary bg-primary/10 ring-4 ring-primary/10'
                : 'border-muted-foreground/25 hover:border-primary/60 hover:bg-muted/30'
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
          {/* 装饰性瞄准背景光 */}
          <div className='pointer-events-none absolute inset-0 flex items-center justify-center opacity-40'>
            <div className='h-36 w-36 rounded-full bg-primary/5 blur-2xl' />
          </div>

          {/* 科技瞄准圆圈与图标 */}
          <div className='relative mb-3 flex h-14 w-14 items-center justify-center rounded-2xl border border-primary/20 bg-primary/10 text-primary shadow-xs transition-transform duration-300 group-hover:scale-105'>
            <UploadCloud className='h-7 w-7 transition-transform duration-200 group-hover:-translate-y-0.5' />
            <div className='absolute -end-1 -top-1 flex h-4 w-4 items-center justify-center rounded-full bg-primary text-[10px] text-primary-foreground'>
              <Sparkles className='h-2.5 w-2.5' />
            </div>
          </div>

          <p className='text-sm font-semibold text-foreground transition-colors group-hover:text-primary'>
            {t('dropzone.prompt')}
          </p>
          <p className='mt-1.5 max-w-xs text-xs leading-relaxed text-muted-foreground'>
            {t('dropzone.formatHint')}
          </p>

          {/* 规范指南胶囊标签 */}
          <div className='mt-4 flex flex-wrap items-center justify-center gap-1.5'>
            <Badge
              variant='secondary'
              className='border border-border/50 bg-background/60 px-2 py-0.5 text-[10px] font-medium text-muted-foreground'
            >
              {t('dropzone.tagFormats')}
            </Badge>
            <Badge
              variant='secondary'
              className='border border-border/50 bg-background/60 px-2 py-0.5 text-[10px] font-medium text-muted-foreground'
            >
              {t('dropzone.tagMaxSize')}
            </Badge>
            <Badge
              variant='secondary'
              className='border border-border/50 bg-background/60 px-2 py-0.5 text-[10px] font-medium text-muted-foreground'
            >
              {t('dropzone.tagFaceReq')}
            </Badge>
          </div>
        </div>
      )}
    </Card>
  )
}
