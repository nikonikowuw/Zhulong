import { useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import {
  AlertCircle,
  CheckCircle2,
  Download,
  FileArchive,
  Loader2,
  RotateCcw,
  Upload,
  XCircle,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
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
import {
  PERSONS_QUERY_KEY,
  useCreateFaceImportJob,
  useFaceImportJob,
} from '../hooks/use-persons'
import { usePersonsContext } from './persons-provider'

function downloadCsv(lines: string[][], filename: string) {
  const content = '\uFEFF' + lines.map((row) => row.join(',')).join('\r\n')
  const blob = new Blob([content], { type: 'text/csv;charset=utf-8;' })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  document.body.appendChild(anchor)
  anchor.click()
  requestAnimationFrame(() => {
    document.body.removeChild(anchor)
    URL.revokeObjectURL(url)
  })
}

function downloadManifestTemplate() {
  const lines = [
    ['name', 'personId', 'imagePath', 'isPrimary'],
    ['张三', 'EMP-001', 'images/EMP001_1.jpg', 'true'],
    ['张三', 'EMP-001', 'images/EMP001_2.jpg', 'false'],
    ['李四', 'EMP-002', 'images/EMP002.png', 'true'],
  ]
  downloadCsv(lines, 'manifest_template.csv')
}

export function PersonsFaceImportDialog() {
  const { t } = useTranslation('persons')
  const { open, setOpen } = usePersonsContext()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [activeJobId, setActiveJobId] = useState<string | null>(null)

  const createMutation = useCreateFaceImportJob()
  const isOpen = open === 'import-faces'

  const jobQuery = useFaceImportJob(activeJobId, isOpen)
  const job = jobQuery.data ?? null
  const queryClient = useQueryClient()

  useEffect(() => {
    if (job?.status === 'COMPLETED') {
      void queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })
    }
  }, [job?.status, queryClient])

  const isTerminal =
    job?.status === 'COMPLETED' ||
    job?.status === 'FAILED' ||
    job?.status === 'CANCELLED'
  const isBusy = createMutation.isPending || (Boolean(job) && !isTerminal)

  const reset = () => {
    setActiveJobId(null)
    createMutation.reset()
    if (fileInputRef.current) fileInputRef.current.value = ''
  }

  const handleClose = () => {
    if (isBusy) {
      toast.info(t('faceImport.runningInBackground', '任务已在后台继续运行'))
    }
    setOpen(null)
  }

  const selectFile = async (selected?: File) => {
    if (!selected) return
    const isZip =
      selected.name.toLowerCase().endsWith('.zip') ||
      selected.type === 'application/zip' ||
      selected.type === 'application/x-zip-compressed'
    if (!isZip) {
      toast.error(t('faceImport.selectZipError', '请选择 ZIP 压缩包文件'))
      return
    }

    try {
      const resp = await createMutation.mutateAsync(selected)
      setActiveJobId(resp.jobId)
      toast.success(
        t('faceImport.jobStarted', '导入任务已创建，后台开始解压处理')
      )
    } catch {
      // Global API error handler displays error toast
    }
  }

  const downloadFailedReport = () => {
    if (!activeJobId) return
    const anchor = document.createElement('a')
    anchor.href = `/api/v1/persons/import-face-jobs/${activeJobId}/failed-report`
    anchor.download = `failed_report_${activeJobId}.csv`
    document.body.appendChild(anchor)
    anchor.click()
    requestAnimationFrame(() => {
      document.body.removeChild(anchor)
    })
  }

  const total = job?.total ?? 0
  const processed = job?.processed ?? 0
  const succeeded = job?.succeeded ?? 0
  const failed = job?.failed ?? 0
  const percent =
    total > 0 ? Math.min(100, Math.round((processed / total) * 100)) : 0

  return (
    <Dialog
      open={isOpen}
      onOpenChange={(nextOpen) => !nextOpen && handleClose()}
    >
      <DialogContent className='max-w-xl'>
        <DialogHeader>
          <DialogTitle>
            {t('faceImport.title', '人员人脸压缩包批量导入')}
          </DialogTitle>
          <DialogDescription>
            {t(
              'faceImport.description',
              '上传包含照片与可选清单的 ZIP 压缩包，自动进行 AI 质检建模并导入底库。'
            )}
          </DialogDescription>
        </DialogHeader>

        {!activeJobId ? (
          <div className='space-y-4'>
            <input
              ref={fileInputRef}
              type='file'
              accept='.zip,application/zip,application/x-zip-compressed'
              className='hidden'
              onChange={(event) => void selectFile(event.target.files?.[0])}
            />

            <div className='grid gap-3 sm:grid-cols-[1fr_auto] sm:items-stretch'>
              <button
                type='button'
                disabled={createMutation.isPending}
                onClick={() => fileInputRef.current?.click()}
                className='flex flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed border-border p-6 text-center transition-colors hover:border-primary hover:bg-muted/40 disabled:cursor-not-allowed disabled:opacity-60'
              >
                {createMutation.isPending ? (
                  <Loader2 className='size-8 animate-spin text-primary' />
                ) : (
                  <Upload className='size-8 text-muted-foreground' />
                )}
                <span className='font-medium text-foreground'>
                  {t('faceImport.chooseZip', '点击选择或拖拽 ZIP 压缩包')}
                </span>
                <span className='text-xs text-muted-foreground'>
                  {t(
                    'faceImport.fileHint',
                    '支持标准 ZIP 格式，文件大小不超过 200MB'
                  )}
                </span>
              </button>

              <Button
                type='button'
                variant='outline'
                className='flex h-auto flex-col items-center justify-center gap-1.5 p-4'
                onClick={downloadManifestTemplate}
              >
                <Download className='size-4' />
                <span>{t('faceImport.downloadTemplate', '下载清单模板')}</span>
                <span className='text-xs text-muted-foreground'>
                  manifest.csv
                </span>
              </Button>
            </div>

            <div className='space-y-1.5 rounded-lg border border-border bg-muted/30 p-3 text-xs text-muted-foreground'>
              <div className='flex items-center gap-1.5 font-medium text-foreground'>
                <FileArchive className='size-3.5' />
                <span>{t('faceImport.specTitle', '压缩包组织标准')}</span>
              </div>
              <ul className='list-inside list-disc space-y-0.5 ps-1'>
                <li>
                  {t(
                    'faceImport.spec1',
                    '清单驱动（推荐）：根目录包含 manifest.csv 与 images/ 图片目录。'
                  )}
                </li>
                <li>
                  {t(
                    'faceImport.spec2',
                    '自适应模式：无清单时，自动根据文件名匹配「工号_姓名.jpg」进行建档。'
                  )}
                </li>
                <li>
                  {t(
                    'faceImport.spec3',
                    'AI 质检门禁：严禁合影照（多张人脸）、无脸照与大侧脸；不合格项将自动隔离并记入失败报告。'
                  )}
                </li>
              </ul>
            </div>
          </div>
        ) : (
          <div className='space-y-4 py-2'>
            {/* 状态徽标与当前阶段 */}
            <div className='flex items-center justify-between'>
              <div className='flex items-center gap-2'>
                {isBusy ? (
                  <Loader2 className='size-5 animate-spin text-primary' />
                ) : job?.status === 'COMPLETED' ? (
                  <CheckCircle2 className='size-5 text-emerald-500' />
                ) : (
                  <XCircle className='size-5 text-destructive' />
                )}
                <span className='font-medium'>
                  {job?.status === 'PENDING' &&
                    t('faceImport.statusPending', '任务排队中...')}
                  {job?.status === 'EXTRACTING' &&
                    t(
                      'faceImport.statusExtracting',
                      '正在安全解压与校验压缩包...'
                    )}
                  {job?.status === 'PROCESSING' &&
                    t(
                      'faceImport.statusProcessing',
                      '正在进行 AI 质检与特征提取...'
                    )}
                  {job?.status === 'COMPLETED' &&
                    t('faceImport.statusCompleted', '导入完成')}
                  {job?.status === 'FAILED' &&
                    t('faceImport.statusFailed', '导入失败')}
                  {job?.status === 'CANCELLED' &&
                    t('faceImport.statusCancelled', '已取消')}
                </span>
              </div>
              <Badge
                variant={job?.status === 'COMPLETED' ? 'default' : 'secondary'}
              >
                {job?.status ?? 'PENDING'}
              </Badge>
            </div>

            {/* 进度条 */}
            <div className='space-y-1.5'>
              <div className='flex justify-between text-xs text-muted-foreground'>
                <span>{t('faceImport.progressLabel', '处理进度')}</span>
                <span>{percent}%</span>
              </div>
              <div className='h-2 w-full overflow-hidden rounded-full bg-secondary'>
                <div
                  className='h-full bg-primary transition-all duration-300'
                  style={{ width: `${percent}%` }}
                />
              </div>
              {job?.currentFile && isBusy && (
                <p className='truncate text-xs text-muted-foreground'>
                  {t('faceImport.currentFile', '正在处理: ')} {job.currentFile}
                </p>
              )}
            </div>

            {/* 数据统计指标 */}
            <div className='grid grid-cols-4 gap-2 text-center'>
              <div className='rounded-md border border-border bg-muted/20 p-2'>
                <div className='text-xs text-muted-foreground'>
                  {t('faceImport.total', '总数')}
                </div>
                <div className='text-lg font-semibold'>{total}</div>
              </div>
              <div className='rounded-md border border-border bg-muted/20 p-2'>
                <div className='text-xs text-muted-foreground'>
                  {t('faceImport.processed', '已处理')}
                </div>
                <div className='text-lg font-semibold'>{processed}</div>
              </div>
              <div className='rounded-md border border-emerald-500/30 bg-emerald-500/10 p-2'>
                <div className='text-xs text-emerald-600 dark:text-emerald-400'>
                  {t('faceImport.succeeded', '成功入库')}
                </div>
                <div className='text-lg font-semibold text-emerald-600 dark:text-emerald-400'>
                  {succeeded}
                </div>
              </div>
              <div className='rounded-md border border-destructive/30 bg-destructive/10 p-2'>
                <div className='text-xs text-destructive'>
                  {t('faceImport.failed', '质检失败')}
                </div>
                <div className='text-lg font-semibold text-destructive'>
                  {failed}
                </div>
              </div>
            </div>

            {/* 失败提示或完成报告 */}
            {job?.status === 'FAILED' && (
              <Alert variant='destructive'>
                <AlertCircle className='size-4' />
                <AlertTitle>
                  {t('faceImport.errorTitle', '任务执行异常')}
                </AlertTitle>
                <AlertDescription>
                  {job.errorMessage || t('faceImport.unknownError', '未知错误')}
                </AlertDescription>
              </Alert>
            )}

            {job?.status === 'COMPLETED' && (
              <Alert className='border-emerald-500/30 bg-emerald-500/5 text-emerald-800 dark:text-emerald-300'>
                <CheckCircle2 className='size-4 text-emerald-600' />
                <AlertTitle>
                  {t('faceImport.completeTitle', '人脸底库批量处理完成')}
                </AlertTitle>
                <AlertDescription>
                  {t('faceImport.completeSummary', {
                    defaultValue:
                      '成功入库 {{succeeded}} 条人脸特征；{{failed}} 条未达质检门禁。',
                    succeeded,
                    failed,
                  })}
                </AlertDescription>
              </Alert>
            )}
          </div>
        )}

        <DialogFooter className='gap-2 sm:justify-between'>
          {activeJobId ? (
            <>
              {isTerminal && failed > 0 ? (
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  onClick={downloadFailedReport}
                  className='border-destructive/30 text-destructive hover:bg-destructive/10'
                >
                  <Download className='me-1.5 size-4' />
                  <span>
                    {t('faceImport.downloadFailedReport', '下载失败报告 (CSV)')}
                  </span>
                </Button>
              ) : (
                <div />
              )}

              <div className='flex gap-2'>
                {isTerminal ? (
                  <>
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      onClick={reset}
                    >
                      <RotateCcw className='me-1.5 size-4' />
                      <span>{t('faceImport.retryAnother', '重新导入')}</span>
                    </Button>
                    <Button type='button' size='sm' onClick={handleClose}>
                      <span>{t('common.done', '完成')}</span>
                    </Button>
                  </>
                ) : (
                  <Button
                    type='button'
                    variant='secondary'
                    size='sm'
                    onClick={handleClose}
                  >
                    <span>{t('faceImport.runInBackground', '后台运行')}</span>
                  </Button>
                )}
              </div>
            </>
          ) : (
            <div className='flex w-full justify-end'>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                onClick={() => setOpen(null)}
              >
                <span>{t('common.cancel', '取消')}</span>
              </Button>
            </div>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
