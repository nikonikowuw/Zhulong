import { useEffect, useMemo, useRef, useState } from 'react'
import {
  AlertTriangle,
  Download,
  FileSpreadsheet,
  FileArchive,
  Loader2,
  RotateCcw,
  X,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Switch } from '@/components/ui/switch'
import { type CameraNameLookup } from '../../data/camera-label'
import { type RecordItem, type RecordsTab } from '../../data/schema'
import { exportDb, type ExportTaskDraft } from '../../lib/export-db'
import {
  DEFAULT_MAX_RECORDS,
  executeRecordsExport,
  ExportLimitError,
  HARD_MAX_IMAGES,
  type ExportLabels,
  type ExportProgress,
  triggerDownload,
} from '../../lib/export-zip-pipeline'

export interface RecordsExportDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** 当前正在浏览的 Tab */
  activeTab: RecordsTab
  /** 导出模式：'selection' 针对勾选列表 | 'batch' 针对当前全局条件 */
  mode: 'selection' | 'batch'
  /** 选中模式下的记录列表 */
  selectedItems?: RecordItem[]
  /** 全局条件模式下的参数快照 */
  queryParams?: {
    cameraId?: string
    startTime?: string
    endTime?: string
    matched?: boolean
    personId?: string
    alarmType?: string
    targetType?: string
  }
  /** 预估总匹配数 (当前 Tab) */
  estimatedTotal?: number
  /** 相机名称解析器 */
  cameraName?: CameraNameLookup
}

const DEFAULT_MAX_IMAGES = 200

function clampMaxImages(value: number): number {
  if (!Number.isFinite(value)) return DEFAULT_MAX_IMAGES
  return Math.min(HARD_MAX_IMAGES, Math.max(1, Math.trunc(value)))
}

export function RecordsExportDialog({
  open,
  onOpenChange,
  activeTab,
  mode,
  selectedItems = [],
  queryParams,
  estimatedTotal = 0,
  cameraName,
}: RecordsExportDialogProps) {
  const { t } = useTranslation('records')

  // 批量模式下的 Tab 维度选择（默认勾选当前 activeTab）
  const [selectedTabs, setSelectedTabs] = useState<Set<RecordsTab>>(
    () => new Set([activeTab])
  )

  // 弹窗打开或 activeTab 变化时在 render 阶段同步重置（符合 React 19 最佳实践）
  const [prevOpen, setPrevOpen] = useState(open)
  const [prevActiveTab, setPrevActiveTab] = useState(activeTab)
  if (open !== prevOpen || activeTab !== prevActiveTab) {
    setPrevOpen(open)
    setPrevActiveTab(activeTab)
    if (open) {
      setSelectedTabs(new Set([activeTab]))
    }
  }

  // 导出内容与配置
  const [exportCsv, setExportCsv] = useState(true)
  const [exportImages, setExportImages] = useState(false)
  const [burnOverlay, setBurnOverlay] = useState(true)
  const [imageKind, setImageKind] = useState<'scene' | 'closeup' | 'both'>(
    'scene'
  )
  const [limitImages, setLimitImages] = useState(true)
  // 文本态而不是 number 态：number 输入框在清空时会被 parseInt 立刻重置成 1，
  // 用户根本没法把 200 改成 50（中间要经过空串）。
  const [maxImagesText, setMaxImagesText] = useState(String(DEFAULT_MAX_IMAGES))
  const maxImages = clampMaxImages(Number.parseInt(maxImagesText, 10))

  // 导出运行态
  const [isExporting, setIsExporting] = useState(false)
  const [progress, setProgress] = useState<ExportProgress | null>(null)
  const abortControllerRef = useRef<AbortController | null>(null)

  // 未完成任务草稿
  const [pendingDraft, setPendingDraft] = useState<ExportTaskDraft | null>(null)

  // 弹窗展开时清理超期草稿并探测是否存在可恢复任务
  useEffect(() => {
    if (!open) return
    let cancelled = false
    void (async () => {
      await exportDb.pruneExpiredTasks().catch(() => undefined)
      const draft = await exportDb.getLatestPendingTask().catch(() => null)
      if (!cancelled) setPendingDraft(draft)
    })()
    return () => {
      cancelled = true
    }
  }, [open])

  // 烧录进证据图的标签跟随界面语言
  const overlayLabels = useMemo<ExportLabels>(
    () => ({
      scene: (ctx) => {
        const percent = Math.round((ctx.similarity ?? ctx.score ?? 0) * 100)
        if (ctx.tab === 'recognitions') {
          if (!ctx.matched) {
            return t('export.overlayStranger', {
              percent,
              defaultValue: `陌生人 (${percent}%)`,
            })
          }
          if (ctx.personName) {
            return t('export.overlayMatched', {
              name: ctx.personName,
              percent,
              defaultValue: `${ctx.personName} (${percent}%)`,
            })
          }
          return t('export.overlayKnown', {
            percent,
            defaultValue: `已命中 (${percent}%)`,
          })
        }
        const subject =
          ctx.tab === 'alarms' ? (ctx.alarmType ?? '') : (ctx.targetType ?? '')
        return `${subject} (${percent}%)`
      },
      person: t('export.overlayPerson', { defaultValue: '人体' }),
    }),
    [t]
  )

  const handleDiscardDraft = async () => {
    if (pendingDraft) {
      await exportDb.clearTaskDraft(pendingDraft.taskId)
      setPendingDraft(null)
      toast.info(t('export.draftDiscarded', { defaultValue: '已清除历史草稿' }))
    }
  }

  const toggleTab = (tabKey: RecordsTab) => {
    setSelectedTabs((prev) => {
      const next = new Set(prev)
      if (next.has(tabKey)) {
        if (next.size > 1) {
          next.delete(tabKey)
        }
      } else {
        next.add(tabKey)
      }
      return next
    })
  }

  const handleCancelExport = () => {
    if (abortControllerRef.current) {
      abortControllerRef.current.abort()
      abortControllerRef.current = null
    }
    setIsExporting(false)
    setProgress(null)
    toast.info(t('export.cancelled', { defaultValue: '已取消导出' }))
  }

  const handleStartExport = async (resumeTaskId?: string) => {
    if (mode === 'selection' && selectedItems.length === 0 && !resumeTaskId) {
      toast.error(
        t('export.emptySelection', { defaultValue: '尚未勾选任何记录' })
      )
      return
    }

    if (!exportCsv && !exportImages) {
      toast.error(
        t('export.selectAtLeastOneFormat', {
          defaultValue: '请至少选择一种导出内容（报表或图片）',
        })
      )
      return
    }

    const controller = new AbortController()
    abortControllerRef.current = controller
    setIsExporting(true)
    setProgress({
      phase: 'preparing',
      current: 0,
      total: 100,
      percent: 0,
      message: t('export.preparing', { defaultValue: '正在准备导出任务...' }),
    })

    try {
      const result = await executeRecordsExport({
        resumeTaskId,
        selectedRecords:
          mode === 'selection'
            ? {
                tab: activeTab,
                items: selectedItems,
              }
            : undefined,
        queryConfig:
          mode === 'batch'
            ? {
                tabs: Array.from(selectedTabs),
                cameraId: queryParams?.cameraId,
                startTime: queryParams?.startTime,
                endTime: queryParams?.endTime,
                matched: queryParams?.matched,
                personId: queryParams?.personId,
                alarmType: queryParams?.alarmType,
                targetType: queryParams?.targetType,
              }
            : undefined,
        exportCsv,
        exportImages,
        burnOverlay,
        imageKind,
        maxImages: limitImages ? maxImages : undefined,
        labels: overlayLabels,
        signal: controller.signal,
        resolveCameraName: cameraName,
        onProgress: setProgress,
      })

      triggerDownload(result.blob, result.filename)
      setPendingDraft(null)

      if (result.failedImages > 0) {
        toast.warning(
          t('export.partialFailure', {
            count: result.failedImages,
            defaultValue: `${result.failedImages} 项图片未能打包，明细见 manifest.json`,
          })
        )
      } else {
        toast.success(
          t('export.success', {
            defaultValue: '导出完成，文件已开始下载',
          })
        )
      }
      onOpenChange(false)
    } catch (err: unknown) {
      if (err instanceof DOMException && err.name === 'AbortError') {
        // 用户主动取消，不报系统异常
        return
      }
      if (err instanceof ExportLimitError) {
        toast.error(
          t('export.tooManyRecords', {
            total: err.actual,
            limit: err.limit,
            defaultValue: `匹配记录 ${err.actual} 条，超过单次导出上限 ${err.limit} 条，请缩小时间或摄像头范围`,
          })
        )
        return
      }
      toast.error(
        t('export.failedWithReason', {
          reason: err instanceof Error ? err.message : String(err),
          defaultValue: `导出失败：${err instanceof Error ? err.message : String(err)}`,
        })
      )
    } finally {
      setIsExporting(false)
      setProgress(null)
      abortControllerRef.current = null
    }
  }

  const percent = progress
    ? (progress.percent ??
      (progress.total > 0
        ? Math.min(100, Math.round((progress.current / progress.total) * 100))
        : 0))
    : 0

  const overRecordBudget =
    mode === 'batch' && estimatedTotal > DEFAULT_MAX_RECORDS

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (isExporting) {
          handleCancelExport()
        }
        onOpenChange(next)
      }}
    >
      <DialogContent className='max-w-md sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle className='flex items-center gap-2 text-lg'>
            <Download className='size-5 text-primary' />
            <span>
              {mode === 'selection'
                ? t('export.titleSelection', {
                    defaultValue: '导出选中感知记录',
                  })
                : t('export.titleBatch', { defaultValue: '批量导出感知记录' })}
            </span>
          </DialogTitle>
          <DialogDescription className='text-xs text-muted-foreground'>
            {mode === 'selection'
              ? t('export.descSelection', {
                  count: selectedItems.length,
                  defaultValue: `已锁定勾选的 ${selectedItems.length} 条记录`,
                })
              : t('export.descBatch', {
                  total: estimatedTotal,
                  defaultValue: `基于当前筛选条件导出（当前 Tab 预估匹配 ${estimatedTotal} 条）`,
                })}
          </DialogDescription>
        </DialogHeader>

        {/* 历史未完成任务断点恢复提示条 */}
        {pendingDraft && !isExporting && (
          <div className='flex animate-in items-center justify-between gap-3 rounded-lg border border-primary/30 bg-primary/5 p-3 text-xs duration-200 fade-in'>
            <div className='flex items-center gap-2'>
              <RotateCcw className='size-4 shrink-0 animate-pulse text-primary' />
              <div>
                <div className='font-semibold text-foreground'>
                  {t('export.draftFoundTitle', {
                    defaultValue: '发现上次未完成的导出任务',
                  })}
                </div>
                <div className='text-[11px] text-muted-foreground'>
                  {t('export.draftFoundDesc', {
                    current: pendingDraft.completedKeys.length,
                    total: pendingDraft.totalCount,
                    defaultValue: `已处理 ${pendingDraft.completedKeys.length} / ${pendingDraft.totalCount} 项证据图片`,
                  })}
                </div>
                <div className='text-[11px] text-muted-foreground'>
                  {t('export.resumeHint', {
                    defaultValue:
                      '续传沿用上次任务的报表与图片清单，当前筛选条件不参与',
                  })}
                </div>
              </div>
            </div>

            <div className='flex items-center gap-1.5'>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                className='h-7 px-2 text-xs text-muted-foreground hover:text-foreground'
                onClick={handleDiscardDraft}
              >
                {t('export.discardDraft', { defaultValue: '放弃' })}
              </Button>
              <Button
                type='button'
                size='sm'
                className='h-7 gap-1 px-2.5 text-xs'
                onClick={() => handleStartExport(pendingDraft.taskId)}
              >
                <RotateCcw className='size-3' />
                <span>
                  {t('export.resumeDraft', { defaultValue: '继续断点导出' })}
                </span>
              </Button>
            </div>
          </div>
        )}

        {/* 正在导出时的进度态 */}
        {isExporting ? (
          <div className='my-4 space-y-4 rounded-lg border border-border bg-muted/20 p-4'>
            <div
              role='status'
              aria-live='polite'
              className='flex items-center justify-between text-xs font-medium'
            >
              <span className='flex items-center gap-2'>
                <Loader2 className='size-3.5 animate-spin text-primary' />
                <span>
                  {progress?.message ||
                    t('export.inProgress', {
                      defaultValue: '正在生成导出文件...',
                    })}
                </span>
              </span>
              <span className='font-mono'>{percent}%</span>
            </div>

            <div
              role='progressbar'
              aria-label={t('export.progressLabel', {
                defaultValue: '导出进度',
              })}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={percent}
              className='h-2 w-full overflow-hidden rounded-full bg-secondary'
            >
              <div
                className='h-full bg-primary transition-all duration-300'
                style={{ width: `${percent}%` }}
              />
            </div>

            <p className='text-center text-[11px] text-muted-foreground'>
              {t('export.offloadHint', {
                defaultValue:
                  '提示：目标检测框正由前端离屏 Canvas 实时合成，零占用边缘宿主算力。',
              })}
            </p>
          </div>
        ) : (
          <div className='space-y-5 py-2 text-sm'>
            {/* 批量模式下的 Tab 自由多维组合 */}
            {mode === 'batch' && (
              <div className='space-y-2'>
                <Label className='text-xs font-semibold text-foreground'>
                  {t('export.dimensionScope', { defaultValue: '导出维度范围' })}
                </Label>
                <div className='grid grid-cols-2 gap-2.5 rounded-lg border border-border p-3'>
                  {[
                    {
                      key: 'recognitions',
                      label: t('tabs.recognitions', {
                        defaultValue: '识别记录',
                      }),
                    },
                    {
                      key: 'alarms',
                      label: t('tabs.alarms', { defaultValue: '告警记录' }),
                    },
                    {
                      key: 'captures',
                      label: t('tabs.captures', { defaultValue: '抓拍记录' }),
                    },
                    {
                      key: 'vehicles',
                      label: t('tabs.vehicles', { defaultValue: '车辆记录' }),
                    },
                  ].map(({ key, label }) => (
                    <label
                      key={key}
                      className='flex cursor-pointer items-center gap-2 text-xs font-medium'
                    >
                      <Checkbox
                        checked={selectedTabs.has(key as RecordsTab)}
                        onCheckedChange={() => toggleTab(key as RecordsTab)}
                      />
                      <span>{label}</span>
                    </label>
                  ))}
                </div>
                {overRecordBudget && (
                  <div
                    role='alert'
                    className='flex items-start gap-2 rounded bg-destructive/10 p-2 text-[11px] text-destructive'
                  >
                    <AlertTriangle className='mt-0.5 size-3.5 shrink-0' />
                    <span>
                      {t('export.overRecordBudget', {
                        total: estimatedTotal,
                        limit: DEFAULT_MAX_RECORDS,
                        defaultValue: `当前 Tab 已匹配 ${estimatedTotal} 条，超过单次导出的 ${DEFAULT_MAX_RECORDS} 条上限，请缩小时间或摄像头范围`,
                      })}
                    </span>
                  </div>
                )}
              </div>
            )}

            {/* 导出内容类型 */}
            <div className='space-y-3'>
              <Label className='text-xs font-semibold text-foreground'>
                {t('export.contentOptions', { defaultValue: '导出内容格式' })}
              </Label>

              <div className='space-y-2.5 rounded-lg border border-border p-3'>
                {/* CSV 开关 */}
                <div className='flex items-center justify-between'>
                  <div className='flex items-center gap-2'>
                    <FileSpreadsheet className='size-4 text-emerald-500' />
                    <div>
                      <Label
                        htmlFor='export-csv'
                        className='cursor-pointer text-xs font-medium'
                      >
                        {t('export.csvReport', {
                          defaultValue: '结构化数据报表 (CSV)',
                        })}
                      </Label>
                      <div className='text-[11px] text-muted-foreground'>
                        {t('export.csvDesc', {
                          defaultValue:
                            '带 UTF-8 BOM，兼容 Windows Excel / WPS 无乱码',
                        })}
                      </div>
                    </div>
                  </div>
                  <Switch
                    id='export-csv'
                    checked={exportCsv}
                    onCheckedChange={setExportCsv}
                  />
                </div>

                <div className='h-px bg-border/60' />

                {/* 图片证据包开关 */}
                <div className='flex items-center justify-between'>
                  <div className='flex items-center gap-2'>
                    <FileArchive className='size-4 text-sky-500' />
                    <div>
                      <Label
                        htmlFor='export-images'
                        className='cursor-pointer text-xs font-medium'
                      >
                        {t('export.imageEvidence', {
                          defaultValue: '抓拍图片证据包 (ZIP)',
                        })}
                      </Label>
                      <div className='text-[11px] text-muted-foreground'>
                        {t('export.imageDesc', {
                          defaultValue:
                            '打包原始画面与特写，可自由选择是否合成目标框',
                        })}
                      </div>
                    </div>
                  </div>
                  <Switch
                    id='export-images'
                    checked={exportImages}
                    onCheckedChange={setExportImages}
                  />
                </div>
              </div>
            </div>

            {/* 图片相关配置（仅在勾选图片导出时呈现） */}
            {exportImages && (
              <div className='animate-in space-y-3 rounded-lg border border-sky-500/20 bg-sky-500/5 p-3 duration-200 fade-in'>
                {/* 检测框烧录开关 */}
                <div className='flex items-center justify-between'>
                  <div>
                    <Label
                      htmlFor='export-burn-overlay'
                      className='cursor-pointer text-xs font-medium text-foreground'
                    >
                      {t('export.burnOverlay', {
                        defaultValue: '在图片上绘制目标检测框与标签',
                      })}
                    </Label>
                    <div className='text-[11px] text-muted-foreground'>
                      {t('export.burnOverlayDesc', {
                        defaultValue:
                          '由浏览器端离屏绘制目标框、置信度与姓名，零消耗边缘算力',
                      })}
                    </div>
                  </div>
                  <Switch
                    id='export-burn-overlay'
                    checked={burnOverlay}
                    onCheckedChange={setBurnOverlay}
                  />
                </div>

                <div className='h-px bg-border/50' />

                {/* 图片种类单选 */}
                <div className='space-y-1.5'>
                  <Label className='text-xs font-medium text-muted-foreground'>
                    {t('export.imageKind', { defaultValue: '导出图片视角' })}
                  </Label>
                  <RadioGroup
                    value={imageKind}
                    onValueChange={(val) =>
                      setImageKind(val as 'scene' | 'closeup' | 'both')
                    }
                    className='grid grid-cols-3 gap-2'
                  >
                    <label className='flex cursor-pointer items-center gap-1.5 text-xs'>
                      <RadioGroupItem value='scene' />
                      <span>
                        {t('export.sceneOnly', { defaultValue: '仅全景大图' })}
                      </span>
                    </label>
                    <label className='flex cursor-pointer items-center gap-1.5 text-xs'>
                      <RadioGroupItem value='closeup' />
                      <span>
                        {t('export.closeupOnly', {
                          defaultValue: '仅特写抠图',
                        })}
                      </span>
                    </label>
                    <label className='flex cursor-pointer items-center gap-1.5 text-xs'>
                      <RadioGroupItem value='both' />
                      <span>
                        {t('export.bothImages', { defaultValue: '两者均导出' })}
                      </span>
                    </label>
                  </RadioGroup>
                </div>

                {/* 图片上限控制 */}
                <div className='h-px bg-border/50' />
                <div className='flex items-center justify-between gap-4'>
                  <div className='flex items-center gap-2'>
                    <Checkbox
                      id='limitImages'
                      checked={limitImages}
                      onCheckedChange={(c) => setLimitImages(Boolean(c))}
                    />
                    <Label
                      htmlFor='limitImages'
                      className='cursor-pointer text-xs font-normal'
                    >
                      {t('export.limitImages', {
                        defaultValue: '限制最大导出图片数',
                      })}
                    </Label>
                  </div>

                  {limitImages && (
                    <div className='flex items-center gap-1.5'>
                      <Input
                        type='number'
                        min={1}
                        max={HARD_MAX_IMAGES}
                        aria-label={t('export.limitImages', {
                          defaultValue: '限制最大导出图片数',
                        })}
                        value={maxImagesText}
                        onChange={(e) => setMaxImagesText(e.target.value)}
                        onBlur={() =>
                          setMaxImagesText(String(clampMaxImages(maxImages)))
                        }
                        className='h-7 w-20 font-mono text-xs'
                      />
                      <span className='text-[11px] text-muted-foreground'>
                        {t('export.unitSheets', { defaultValue: '张' })}
                      </span>
                    </div>
                  )}
                </div>

                <p className='text-[11px] text-muted-foreground'>
                  {t('export.hardCapHint', {
                    limit: HARD_MAX_IMAGES,
                    defaultValue: `取消限额时仍受单次 ${HARD_MAX_IMAGES} 张上限保护`,
                  })}
                </p>

                {/* 耗时与体积提示 */}
                {estimatedTotal > 200 && (
                  <div className='flex items-start gap-2 rounded bg-amber-500/10 p-2 text-[11px] text-amber-600 dark:text-amber-400'>
                    <AlertTriangle className='mt-0.5 size-3.5 shrink-0' />
                    <span>
                      {t('export.largeVolumeWarning', {
                        defaultValue:
                          '当前匹配图片较多，批量抓取与打包可能需要数十秒，建议保持限额以快速下载。',
                      })}
                    </span>
                  </div>
                )}
              </div>
            )}
          </div>
        )}

        <DialogFooter className='gap-2 sm:gap-0'>
          {isExporting ? (
            <Button
              type='button'
              variant='destructive'
              size='sm'
              onClick={handleCancelExport}
              className='gap-1.5'
            >
              <X className='size-3.5' />
              <span>{t('export.cancelBtn', { defaultValue: '取消导出' })}</span>
            </Button>
          ) : (
            <>
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={() => onOpenChange(false)}
              >
                {t('export.cancel', { defaultValue: '取消' })}
              </Button>
              <Button
                type='button'
                size='sm'
                onClick={() => handleStartExport()}
                className='gap-1.5'
              >
                <Download className='size-3.5' />
                <span>
                  {t('export.confirmBtn', { defaultValue: '开始导出' })}
                </span>
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
