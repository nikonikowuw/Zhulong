import { useEffect, useMemo, useRef, useState } from 'react'
import { Download, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { triggerDownload } from '@/lib/csv'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { type CameraNameLookup } from '../../data/camera-label'
import { type RecordItem, type RecordsTab } from '../../data/schema'
import { exportDb, type ExportTaskDraft } from '../../lib/export-db'
import {
  executeRecordsExport,
  ExportLimitError,
  HARD_MAX_IMAGES,
  type ExportLabels,
  type ExportProgress,
} from '../../lib/export-zip-pipeline'
import { RecordsExportOptionsPanel } from './records-export-options-panel'
import {
  ExportDraftBanner,
  ExportProgressPanel,
} from './records-export-progress'

export interface RecordsExportDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  activeTab: RecordsTab
  mode: 'selection' | 'batch'
  selectedItems?: RecordItem[]
  queryParams?: {
    cameraId?: string
    startTime?: string
    endTime?: string
    matched?: boolean
    personId?: string
    alarmType?: string
    targetType?: string
  }
  estimatedTotal?: number
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

  const [selectedTabs, setSelectedTabs] = useState<Set<RecordsTab>>(
    () => new Set([activeTab])
  )

  const [prevOpen, setPrevOpen] = useState(open)
  const [prevActiveTab, setPrevActiveTab] = useState(activeTab)
  if (open !== prevOpen || activeTab !== prevActiveTab) {
    setPrevOpen(open)
    setPrevActiveTab(activeTab)
    if (open) {
      setSelectedTabs(new Set([activeTab]))
    }
  }

  const [exportCsv, setExportCsv] = useState(true)
  const [exportImages, setExportImages] = useState(false)
  const [burnOverlay, setBurnOverlay] = useState(true)
  const [imageKind, setImageKind] = useState<'scene' | 'closeup' | 'both'>(
    'scene'
  )
  const [limitImages, setLimitImages] = useState(true)
  const [maxImagesText, setMaxImagesText] = useState(String(DEFAULT_MAX_IMAGES))
  const maxImages = clampMaxImages(Number.parseInt(maxImagesText, 10))

  const [isExporting, setIsExporting] = useState(false)
  const [progress, setProgress] = useState<ExportProgress | null>(null)
  const abortControllerRef = useRef<AbortController | null>(null)
  const [pendingDraft, setPendingDraft] = useState<ExportTaskDraft | null>(null)

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

  const overlayLabels = useMemo<ExportLabels>(
    () => ({
      scene: (ctx) => {
        const percent = Math.round((ctx.similarity ?? ctx.score ?? 0) * 100)
        if (ctx.tab === 'recognitions') {
          if (!ctx.matched) {
            return t('export.overlayStranger', { percent })
          }
          if (ctx.personName) {
            return t('export.overlayMatched', {
              name: ctx.personName,
              percent,
            })
          }
          return t('export.overlayKnown', { percent })
        }
        const subject =
          ctx.tab === 'alarms' ? (ctx.alarmType ?? '') : (ctx.targetType ?? '')
        return `${subject} (${percent}%)`
      },
      person: t('export.overlayPerson'),
    }),
    [t]
  )

  const handleDiscardDraft = async () => {
    if (pendingDraft) {
      await exportDb.clearTaskDraft(pendingDraft.taskId)
      setPendingDraft(null)
      toast.info(t('export.draftDiscarded'))
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
    toast.info(t('export.cancelled'))
  }

  const handleStartExport = async (resumeTaskId?: string) => {
    if (mode === 'selection' && selectedItems.length === 0 && !resumeTaskId) {
      toast.error(t('export.emptySelection'))
      return
    }

    if (!exportCsv && !exportImages) {
      toast.error(t('export.selectAtLeastOneFormat'))
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
      message: t('export.preparing'),
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

      if (controller.signal.aborted) {
        return
      }

      triggerDownload(result.blob, result.filename)
      setPendingDraft(null)

      if (result.failedImages > 0) {
        toast.warning(
          t('export.partialFailure', { count: result.failedImages })
        )
      } else {
        toast.success(t('export.success'))
      }
      onOpenChange(false)
    } catch (err: unknown) {
      if (err instanceof DOMException && err.name === 'AbortError') {
        return
      }
      if (err instanceof ExportLimitError) {
        toast.error(
          t('export.tooManyRecords', {
            total: err.actual,
            limit: err.limit,
          })
        )
        return
      }
      toast.error(
        t('export.failedWithReason', {
          reason: err instanceof Error ? err.message : String(err),
        })
      )
    } finally {
      if (abortControllerRef.current === controller) {
        setIsExporting(false)
        setProgress(null)
        abortControllerRef.current = null
      }
    }
  }

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
                ? t('export.titleSelection')
                : t('export.titleBatch')}
            </span>
          </DialogTitle>
          <DialogDescription className='text-xs text-muted-foreground'>
            {mode === 'selection'
              ? t('export.descSelection', { count: selectedItems.length })
              : t('export.descBatch', { total: estimatedTotal })}
          </DialogDescription>
        </DialogHeader>

        {pendingDraft && !isExporting && (
          <ExportDraftBanner
            draft={pendingDraft}
            onDiscard={handleDiscardDraft}
            onResume={() => void handleStartExport(pendingDraft.taskId)}
          />
        )}

        {isExporting ? (
          <ExportProgressPanel progress={progress} />
        ) : (
          <RecordsExportOptionsPanel
            mode={mode}
            estimatedTotal={estimatedTotal}
            selectedTabs={selectedTabs}
            onToggleTab={toggleTab}
            exportCsv={exportCsv}
            onExportCsvChange={setExportCsv}
            exportImages={exportImages}
            onExportImagesChange={setExportImages}
            burnOverlay={burnOverlay}
            onBurnOverlayChange={setBurnOverlay}
            imageKind={imageKind}
            onImageKindChange={setImageKind}
            limitImages={limitImages}
            onLimitImagesChange={setLimitImages}
            maxImagesText={maxImagesText}
            onMaxImagesTextChange={setMaxImagesText}
            onMaxImagesBlur={() =>
              setMaxImagesText(String(clampMaxImages(maxImages)))
            }
          />
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
              <span>{t('export.cancelBtn')}</span>
            </Button>
          ) : (
            <>
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={() => onOpenChange(false)}
              >
                {t('export.cancel')}
              </Button>
              <Button
                type='button'
                size='sm'
                onClick={() => void handleStartExport()}
                className='gap-1.5'
              >
                <Download className='size-3.5' />
                <span>{t('export.confirmBtn')}</span>
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
