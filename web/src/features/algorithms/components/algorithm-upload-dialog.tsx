import { useState, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  UploadCloud,
  FileArchive,
  AlertCircle,
  Loader2,
  X,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { useInstallAlgorithm } from '../hooks/use-algorithms'

interface AlgorithmUploadDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const MAX_FILE_SIZE = 500 * 1024 * 1024 // 500 MB

export function AlgorithmUploadDialog({
  open,
  onOpenChange,
}: AlgorithmUploadDialogProps): React.JSX.Element {
  const { t } = useTranslation('algorithms')
  const [selectedFile, setSelectedFile] = useState<File | null>(null)
  const [uploadProgress, setUploadProgress] = useState<number | null>(null)
  const [errorMessage, setErrorMessage] = useState<string | null>(null)
  const [isDragOver, setIsDragOver] = useState(false)
  const fileInputRef = useRef<HTMLInputElement | null>(null)

  const installMutation = useInstallAlgorithm({ suppressGlobalToast: true })

  const handleFileSelect = (file: File) => {
    setErrorMessage(null)
    const validExtensions = ['.tar.gz', '.tgz', '.zip']
    const hasValidExt = validExtensions.some((ext) =>
      file.name.toLowerCase().endsWith(ext)
    )

    if (!hasValidExt) {
      setErrorMessage(t('invalidFileFormat', '仅支持 .tar.gz 或 .zip 格式的算法包归档文件'))
      return
    }

    if (file.size > MAX_FILE_SIZE) {
      setErrorMessage(t('fileTooLarge', '算法包文件超过 500MB 最大限制'))
      return
    }

    setSelectedFile(file)
  }

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault()
    setIsDragOver(false)
    if (e.dataTransfer.files && e.dataTransfer.files.length > 0) {
      handleFileSelect(e.dataTransfer.files[0])
    }
  }

  const handleUpload = () => {
    if (!selectedFile) return
    setErrorMessage(null)
    setUploadProgress(0)

    installMutation.mutate(
      {
        file: selectedFile,
        onProgress: (percent) => {
          setUploadProgress(percent)
        },
      },
      {
        onSuccess: () => {
          setSelectedFile(null)
          setUploadProgress(null)
          toast.success(
            t('installSuccess', { defaultValue: '算法包安装成功并完成黑盒验证' })
          )
          onOpenChange(false)
        },
        onError: (err) => {
          setUploadProgress(null)
          setErrorMessage(
            err instanceof Error ? err.message : t('uploadFailed', '算法包安装失败')
          )
        },
      }
    )
  }

  const handleClose = () => {
    if (installMutation.isPending) return
    setSelectedFile(null)
    setUploadProgress(null)
    setErrorMessage(null)
    onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent className='sm:max-w-md'>
        <DialogHeader>
          <DialogTitle>{t('uploadDialogTitle', '安装算法包')}</DialogTitle>
          <DialogDescription>
            {t(
              'uploadDialogDesc',
              '上传符合平台规范的算法包归档（.tar.gz 或 .zip，最大 500MB），系统将自动进行黑盒验包与架构匹配。'
            )}
          </DialogDescription>
        </DialogHeader>

        <div className='flex flex-col gap-4 py-2'>
          {/* 拖拽上传区域 */}
          <div
            onDragOver={(e) => {
              e.preventDefault()
              setIsDragOver(true)
            }}
            onDragLeave={() => setIsDragOver(false)}
            onDrop={handleDrop}
            onClick={() => fileInputRef.current?.click()}
            className={`flex cursor-pointer flex-col items-center justify-center rounded-xl border-2 border-dashed p-6 text-center transition-all ${
              isDragOver
                ? 'border-primary bg-primary/10'
                : 'border-border/80 bg-muted/30 hover:border-border hover:bg-muted/50'
            }`}
          >
            <input
              ref={fileInputRef}
              type='file'
              accept='.tar.gz,.tgz,.zip'
              className='hidden'
              onChange={(e) => {
                if (e.target.files && e.target.files.length > 0) {
                  handleFileSelect(e.target.files[0])
                }
              }}
            />

            <div className='mb-2 rounded-full bg-primary/10 p-3 text-primary'>
              <UploadCloud className='h-6 w-6' />
            </div>
            <p className='text-xs font-semibold text-foreground'>
              {t('clickOrDragToUpload', '点击选择或将算法包拖拽至此处')}
            </p>
            <p className='mt-1 text-[11px] text-muted-foreground'>
              {t('supportedFormats', '支持 .tar.gz / .zip 格式，单包 ≤ 500MB')}
            </p>
          </div>

          {/* 已选中文件提示 */}
          {selectedFile && (
            <div className='flex items-center justify-between rounded-lg border border-border bg-card p-2.5 text-xs'>
              <div className='flex items-center gap-2 min-w-0'>
                <FileArchive className='h-4 w-4 shrink-0 text-primary' />
                <span className='truncate font-medium text-foreground'>
                  {selectedFile.name}
                </span>
                <span className='shrink-0 text-muted-foreground'>
                  ({(selectedFile.size / (1024 * 1024)).toFixed(1)} MB)
                </span>
              </div>
              {!installMutation.isPending && (
                <Button
                  variant='ghost'
                  size='icon'
                  className='h-6 w-6 text-muted-foreground hover:text-foreground'
                  onClick={(e) => {
                    e.stopPropagation()
                    setSelectedFile(null)
                  }}
                >
                  <X className='h-3.5 w-3.5' />
                </Button>
              )}
            </div>
          )}

          {/* 上传进度条 */}
          {uploadProgress !== null && (
            <div className='flex flex-col gap-1.5'>
              <div className='flex justify-between text-xs text-muted-foreground'>
                <span>{t('uploading', '正在上传并校验算法包...')}</span>
                <span className='font-mono font-medium'>{uploadProgress}%</span>
              </div>
              <div className='h-2 w-full overflow-hidden rounded-full bg-muted'>
                <div
                  className='h-full bg-primary transition-all duration-200'
                  style={{ width: `${uploadProgress}%` }}
                />
              </div>
            </div>
          )}

          {/* 错误提示 */}
          {errorMessage && (
            <div className='flex items-start gap-2 rounded-lg border border-destructive/20 bg-destructive/10 p-2.5 text-xs text-destructive'>
              <AlertCircle className='h-4 w-4 shrink-0 mt-0.5' />
              <div className='min-w-0 flex-1 break-words'>{errorMessage}</div>
            </div>
          )}
        </div>

        <DialogFooter className='gap-2 sm:gap-0'>
          <Button
            type='button'
            variant='outline'
            onClick={handleClose}
            disabled={installMutation.isPending}
          >
            {t('cancel', '取消')}
          </Button>
          <Button
            type='button'
            onClick={handleUpload}
            disabled={!selectedFile || installMutation.isPending}
          >
            {installMutation.isPending ? (
              <>
                <Loader2 className='me-2 h-4 w-4 animate-spin' />
                {t('installing', '正在安装...')}
              </>
            ) : (
              t('startInstall', '开始安装')
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
