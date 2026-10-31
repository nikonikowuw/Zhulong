import { useState, useRef, useEffect, useCallback } from 'react'
import {
  FlaskConical,
  UploadCloud,
  AlertCircle,
  Loader2,
  Download,
  RotateCcw,
  Maximize2,
  Clock,
  Target,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { type Algorithm, type AlgorithmTrialResult } from '../data/schema'
import { useTrialAlgorithm } from '../hooks/use-algorithms'

interface AlgorithmTrialDialogProps {
  algorithm: Algorithm | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

const MAX_IMAGE_SIZE = 10 * 1024 * 1024 // 10MB
const MAX_IMAGE_DIMENSION = 8192

export function AlgorithmTrialDialog({
  algorithm,
  open,
  onOpenChange,
}: AlgorithmTrialDialogProps): React.JSX.Element {
  const { t } = useTranslation('algorithms')
  const [selectedFile, setSelectedFile] = useState<File | null>(null)
  const [errorMessage, setErrorMessage] = useState<string | null>(null)
  const [isDragOver, setIsDragOver] = useState(false)
  const [trialResult, setTrialResult] = useState<AlgorithmTrialResult | null>(
    null
  )

  const fileInputRef = useRef<HTMLInputElement | null>(null)
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const loadedImageRef = useRef<HTMLImageElement | null>(null)
  const previewUrlRef = useRef<string | null>(null)

  const trialMutation = useTrialAlgorithm({ suppressGlobalToast: true })

  // Clean up object URLs
  const clearPreview = useCallback(() => {
    if (previewUrlRef.current) {
      URL.revokeObjectURL(previewUrlRef.current)
      previewUrlRef.current = null
    }
    loadedImageRef.current = null
    setSelectedFile(null)
    setTrialResult(null)
    setErrorMessage(null)
    if (fileInputRef.current) {
      fileInputRef.current.value = ''
    }
  }, [])

  useEffect(() => {
    if (!open) {
      clearPreview()
    }
  }, [open, clearPreview])

  useEffect(() => {
    return () => {
      if (previewUrlRef.current) {
        URL.revokeObjectURL(previewUrlRef.current)
        previewUrlRef.current = null
      }
    }
  }, [])

  // Canvas drawing routine
  const renderCanvas = useCallback(
    (img: HTMLImageElement, result: AlgorithmTrialResult | null) => {
      const canvas = canvasRef.current
      if (!canvas) return

      canvas.width = img.naturalWidth
      canvas.height = img.naturalHeight

      const ctx = canvas.getContext('2d')
      if (!ctx) return

      // Draw base image
      ctx.drawImage(img, 0, 0, canvas.width, canvas.height)

      if (!result) return

      const w = canvas.width
      const h = canvas.height
      const scale = Math.max(1, Math.min(w, h) / 900)
      const lineWidth = Math.max(2, Math.round(2.5 * scale))
      const fontSize = Math.max(11, Math.round(13 * scale))

      const drawBoxWithLabel = (
        box: [number, number, number, number],
        color: string,
        label: string
      ) => {
        const x1 = Math.max(0, Math.min(1, box[0])) * w
        const y1 = Math.max(0, Math.min(1, box[1])) * h
        const x2 = Math.max(0, Math.min(1, box[2])) * w
        const y2 = Math.max(0, Math.min(1, box[3])) * h
        const bw = x2 - x1
        const bh = y2 - y1

        // Stroke box
        ctx.strokeStyle = color
        ctx.lineWidth = lineWidth
        ctx.strokeRect(x1, y1, bw, bh)

        if (!label) return

        // Label pill
        ctx.font = `600 ${fontSize}px system-ui, -apple-system, sans-serif`
        const textMetrics = ctx.measureText(label)
        const padX = 6 * scale
        const padY = 3 * scale
        const pillW = textMetrics.width + padX * 2
        const pillH = fontSize + padY * 2

        let pillX = x1
        let pillY = y1 - pillH
        if (pillY < 0) {
          pillY = y1
        }
        if (pillX + pillW > w) {
          pillX = Math.max(0, w - pillW)
        }

        ctx.fillStyle = color
        ctx.fillRect(pillX, pillY, pillW, pillH)

        ctx.fillStyle = '#ffffff'
        ctx.textBaseline = 'top'
        ctx.fillText(label, pillX + padX, pillY + padY)
      }

      // 1. Detections (通用检测: 蓝色框 #3b82f6)
      result.detections.forEach((det) => {
        const scoreText = (det.score * 100).toFixed(1)
        drawBoxWithLabel(det.box, '#3b82f6', `${det.label} ${scoreText}%`)
      })

      // 2. Classifications (画布左上角叠加提示)
      if (result.classifications.length > 0) {
        let classOffset = 16 * scale
        result.classifications.forEach((cls) => {
          const text = `${cls.label}: ${(cls.score * 100).toFixed(1)}%`
          ctx.font = `bold ${fontSize + 2}px system-ui, -apple-system, sans-serif`
          const m = ctx.measureText(text)
          const pw = m.width + 16 * scale
          const ph = fontSize + 10 * scale

          ctx.fillStyle = 'rgba(0, 0, 0, 0.7)'
          ctx.fillRect(16 * scale, classOffset, pw, ph)
          ctx.fillStyle = '#38bdf8'
          ctx.textBaseline = 'top'
          ctx.fillText(text, 24 * scale, classOffset + 4 * scale)

          classOffset += ph + 8 * scale
        })
      }

      // 3. Persons & Faces (人体全身: 绿色 #10b981; 人脸: 橙色 #f59e0b)
      result.persons.forEach((person) => {
        // 判断是否属于纯人脸退化语义 (IsFaceCloseup)
        const isFaceCloseup =
          person.faces.length === 1 &&
          Math.abs(person.body_box[0] - person.faces[0].box[0]) < 1e-4 &&
          Math.abs(person.body_box[1] - person.faces[0].box[1]) < 1e-4 &&
          Math.abs(person.body_box[2] - person.faces[0].box[2]) < 1e-4 &&
          Math.abs(person.body_box[3] - person.faces[0].box[3]) < 1e-4

        if (!isFaceCloseup) {
          const scoreText = (person.body_score * 100).toFixed(0)
          drawBoxWithLabel(person.body_box, '#10b981', `Person ${scoreText}%`)
        }

        person.faces.forEach((face) => {
          const qualText = face.quality
            ? ` ${(face.quality * 100).toFixed(0)}%`
            : ''
          const matchText = face.person_name ? ` (${face.person_name})` : ''
          drawBoxWithLabel(face.box, '#f59e0b', `Face${qualText}${matchText}`)

          // 绘制关键点 (landmarks)
          if (face.landmarks && face.landmarks.length > 0) {
            ctx.fillStyle = '#f59e0b'
            const radius = Math.max(2, 3 * scale)
            face.landmarks.forEach(([lx, ly]) => {
              const px = lx * w
              const py = ly * h
              ctx.beginPath()
              ctx.arc(px, py, radius, 0, Math.PI * 2)
              ctx.fill()
            })
          }
        })
      })

      // 4. Vehicles & Plates (车辆: 绿色 #10b981; 车牌: 青色 #06b6d4)
      result.vehicles.forEach((veh) => {
        const scoreText = (veh.vehicle_score * 100).toFixed(0)
        const typeText = veh.vehicle_type ? ` ${veh.vehicle_type}` : ''
        drawBoxWithLabel(
          veh.vehicle_box,
          '#10b981',
          `Vehicle${typeText} ${scoreText}%`
        )

        veh.plates.forEach((plate) => {
          const colorText = plate.plate_color ? ` [${plate.plate_color}]` : ''
          drawBoxWithLabel(
            plate.box,
            '#06b6d4',
            `${plate.plate_number || 'Plate'}${colorText}`
          )
        })
      })
    },
    []
  )

  const handleFileSelect = (file: File) => {
    const validExtensions = ['.jpg', '.jpeg', '.png']
    const hasValidExt = validExtensions.some((ext) =>
      file.name.toLowerCase().endsWith(ext)
    )

    if (!hasValidExt) {
      clearPreview()
      setErrorMessage(t('trialErrorFileFormat', '仅支持 JPEG 或 PNG 格式图片'))
      return
    }

    if (file.size > MAX_IMAGE_SIZE) {
      clearPreview()
      setErrorMessage(t('trialErrorFileSize', '图片大小不能超过 10MB'))
      return
    }

    setErrorMessage(null)
    setTrialResult(null)

    const objectUrl = URL.createObjectURL(file)
    const img = new Image()
    img.onload = () => {
      if (
        img.naturalWidth > MAX_IMAGE_DIMENSION ||
        img.naturalHeight > MAX_IMAGE_DIMENSION
      ) {
        URL.revokeObjectURL(objectUrl)
        clearPreview()
        setErrorMessage(
          t('IMAGE_DIMENSIONS_TOO_LARGE', {
            defaultValue: `图片分辨率超过 ${MAX_IMAGE_DIMENSION}px 限制`,
          })
        )
        return
      }

      if (previewUrlRef.current) {
        URL.revokeObjectURL(previewUrlRef.current)
      }
      previewUrlRef.current = objectUrl
      loadedImageRef.current = img
      setSelectedFile(file)
      // Render initial image to canvas
      requestAnimationFrame(() => renderCanvas(img, null))
    }
    img.onerror = () => {
      URL.revokeObjectURL(objectUrl)
      clearPreview()
      setErrorMessage(t('trialErrorFileFormat', '仅支持 JPEG 或 PNG 格式图片'))
    }
    img.src = objectUrl
  }

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault()
    setIsDragOver(false)
    if (e.dataTransfer.files && e.dataTransfer.files.length > 0) {
      handleFileSelect(e.dataTransfer.files[0])
    }
  }

  const handleRunTrial = () => {
    if (!algorithm || !selectedFile) return
    setErrorMessage(null)

    trialMutation.mutate(
      {
        id: algorithm.id,
        file: selectedFile,
      },
      {
        onSuccess: (data) => {
          setTrialResult(data)
          if (loadedImageRef.current) {
            renderCanvas(loadedImageRef.current, data)
          }
        },
        onError: (err) => {
          const msg =
            err instanceof Error
              ? err.message
              : t('TRIAL_EXECUTION_FAILED', '算法试跑推理执行失败')
          setErrorMessage(msg)
        },
      }
    )
  }

  const handleDownload = () => {
    if (!canvasRef.current || !algorithm) return
    canvasRef.current.toBlob(
      (blob) => {
        if (!blob) return
        const url = URL.createObjectURL(blob)
        const link = document.createElement('a')
        link.href = url
        link.download = `trial_${algorithm.id}_${Date.now()}.jpg`
        document.body.appendChild(link)
        link.click()
        document.body.removeChild(link)
        URL.revokeObjectURL(url)
      },
      'image/jpeg',
      0.92
    )
  }

  const handleClose = () => {
    if (trialMutation.isPending) return
    clearPreview()
    onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent className='flex max-h-[90vh] flex-col p-6 sm:max-w-3xl'>
        <DialogHeader>
          <div className='flex items-center gap-2'>
            <FlaskConical className='size-5 text-primary' />
            <DialogTitle>
              {t('trialDialogTitle', '算法效果试跑')}
              {algorithm && ` - ${algorithm.name} (v${algorithm.version})`}
            </DialogTitle>
          </div>
          <DialogDescription>
            {t(
              'trialDialogDesc',
              '上传单张静态图片（JPEG / PNG，≤ 10MB），在独立隔离进程中执行单次推理并直观预览检测框与属性。仅支持已停用的算法包。'
            )}
          </DialogDescription>
        </DialogHeader>

        <div className='flex-1 space-y-4 overflow-y-auto py-2'>
          {/* Upload Dropzone */}
          {!selectedFile ? (
            <div
              onDragOver={(e) => {
                e.preventDefault()
                setIsDragOver(true)
              }}
              onDragLeave={() => setIsDragOver(false)}
              onDrop={handleDrop}
              onClick={() => fileInputRef.current?.click()}
              className={`flex cursor-pointer flex-col items-center justify-center gap-3 rounded-lg border-2 border-dashed p-10 text-center transition-colors ${
                isDragOver
                  ? 'border-primary bg-primary/5'
                  : 'border-muted-foreground/25 hover:border-primary/50 hover:bg-muted/50'
              }`}
            >
              <input
                ref={fileInputRef}
                type='file'
                accept='.jpg,.jpeg,.png,image/jpeg,image/png'
                className='hidden'
                onChange={(e) => {
                  if (e.target.files && e.target.files.length > 0) {
                    handleFileSelect(e.target.files[0])
                  }
                }}
              />
              <div className='rounded-full bg-primary/10 p-3 text-primary'>
                <UploadCloud className='size-8' />
              </div>
              <div className='space-y-1'>
                <p className='text-sm font-medium'>
                  {t('trialUploadHint', '点击选择或将图片拖拽至此处')}
                </p>
                <p className='text-xs text-muted-foreground'>
                  {t(
                    'trialSupportedFormats',
                    '支持 JPG / JPEG / PNG 格式，分辨率 ≤ 8192px，体积 ≤ 10MB'
                  )}
                </p>
              </div>
            </div>
          ) : (
            <div className='space-y-3'>
              {/* Top Meta Bar */}
              <div className='flex flex-wrap items-center justify-between gap-2 rounded-md bg-muted/50 px-3 py-2 text-xs'>
                <div className='flex items-center gap-3 font-mono'>
                  <span className='max-w-[200px] truncate font-sans font-medium text-foreground'>
                    {selectedFile.name}
                  </span>
                  {trialResult && (
                    <>
                      <span className='flex items-center gap-1 text-muted-foreground'>
                        <Maximize2 className='size-3 text-blue-500' />
                        {trialResult.width} × {trialResult.height}
                      </span>
                      <span className='flex items-center gap-1 text-muted-foreground'>
                        <Clock className='size-3 text-amber-500' />
                        {trialResult.costMs} ms
                      </span>
                      <span className='flex items-center gap-1 text-muted-foreground'>
                        <Target className='size-3 text-emerald-500' />
                        {t('trialTargetCount', '检出目标')}: {trialResult.count}
                      </span>
                    </>
                  )}
                </div>

                <Button
                  variant='ghost'
                  size='sm'
                  onClick={clearPreview}
                  disabled={trialMutation.isPending}
                  className='h-7 gap-1 px-2 text-xs'
                >
                  <RotateCcw className='size-3' />
                  {t('trialReselect', '重新选图')}
                </Button>
              </div>

              {/* Canvas Preview Container */}
              <div className='relative flex max-h-[500px] min-h-[300px] items-center justify-center overflow-hidden rounded-lg border bg-black/5 dark:bg-black/30'>
                <canvas
                  ref={canvasRef}
                  className='max-h-[500px] max-w-full rounded object-contain'
                />

                {trialMutation.isPending && (
                  <div className='absolute inset-0 flex flex-col items-center justify-center gap-2 bg-background/60 backdrop-blur-xs'>
                    <Loader2 className='size-8 animate-spin text-primary' />
                    <p className='text-sm font-medium text-foreground'>
                      {t('trialRunning', '正在推理中...')}
                    </p>
                  </div>
                )}
              </div>

              {trialResult && trialResult.classifications.length > 0 && (
                <div className='flex flex-wrap items-center gap-2 rounded-md border bg-muted/30 p-2 text-xs'>
                  <span className='font-medium text-muted-foreground'>
                    {t('classifications', '分类结果')}:
                  </span>
                  {trialResult.classifications.map((cls, idx) => (
                    <Badge
                      key={idx}
                      variant='secondary'
                      className='gap-1 font-mono text-xs'
                    >
                      <span>{cls.label}</span>
                      <span className='font-semibold text-primary'>
                        {(cls.score * 100).toFixed(1)}%
                      </span>
                    </Badge>
                  ))}
                </div>
              )}

              {trialResult && trialResult.count === 0 && (
                <div className='py-1 text-center'>
                  <Badge variant='outline' className='text-muted-foreground'>
                    {t('trialNoTargets', '未检出任何目标')}
                  </Badge>
                </div>
              )}
            </div>
          )}

          {/* Error Alert */}
          {errorMessage && (
            <Alert variant='destructive'>
              <AlertCircle className='size-4' />
              <AlertDescription>{errorMessage}</AlertDescription>
            </Alert>
          )}
        </div>

        <DialogFooter className='items-center gap-2 pt-2 sm:justify-between'>
          <div>
            {trialResult && (
              <Button
                variant='outline'
                size='sm'
                onClick={handleDownload}
                className='gap-1.5'
              >
                <Download className='size-4' />
                {t('trialDownloadResult', '下载标注图')}
              </Button>
            )}
          </div>

          <div className='flex items-center gap-2'>
            <Button
              variant='outline'
              onClick={handleClose}
              disabled={trialMutation.isPending}
            >
              {t('cancel', '取消')}
            </Button>

            {selectedFile && !trialResult && (
              <Button
                onClick={handleRunTrial}
                disabled={trialMutation.isPending}
                className='gap-1.5'
              >
                {trialMutation.isPending ? (
                  <>
                    <Loader2 className='size-4 animate-spin' />
                    {t('trialRunning', '正在推理中...')}
                  </>
                ) : (
                  <>
                    <FlaskConical className='size-4' />
                    {t('trialStart', '开始试跑')}
                  </>
                )}
              </Button>
            )}
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
