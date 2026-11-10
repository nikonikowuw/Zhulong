import { useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import {
  ALGORITHMS_QUERY_KEY,
  algorithmsApi,
  type Algorithm,
} from '@/features/algorithms/contract'
import { type Camera as CameraSchema } from '@/features/cameras/contract'
import { type FrameSurfaceState } from '@/features/live'
import {
  buildConfigFormSchema,
  isZeroConfig,
  parseSchema,
} from '../data/algo-config-schema'
import {
  calculatePeakCompute,
  isScheduleActiveAt,
  type SchedulableItem,
} from '../data/schedule-config'
import { type Task } from '../data/schema'
import { useChannelTaskDrafts } from '../hooks/use-channel-task-drafts'
import { useSyncChannelAlgorithms } from '../hooks/use-sync-channel-algorithms'
import {
  DEFAULT_COMPUTE_COST,
  MAX_COMPUTE_POINTS,
} from '../hooks/use-task-compute-budget'
import { useDeleteTask } from '../hooks/use-tasks'
import { ChannelTaskAlgoPanel } from './channel-task-algo-panel'
import { ChannelTaskDialogFooter } from './channel-task-dialog-footer'
import { ChannelTaskDialogHeader } from './channel-task-dialog-header'
import { ChannelTaskRegionViewport } from './channel-task-region-viewport'

export interface ChannelTaskDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  initialCameraId?: string
  initialAlgorithmId?: string
  cameras: CameraSchema[]
  algorithms: Algorithm[]
  allTasks: Task[]
}

interface ChannelTaskDialogContentProps extends Omit<
  ChannelTaskDialogProps,
  'open' | 'onOpenChange'
> {
  onClose: () => void
}

function ChannelTaskDialogContent({
  initialCameraId,
  initialAlgorithmId,
  cameras,
  algorithms,
  allTasks,
  onClose,
}: ChannelTaskDialogContentProps): React.JSX.Element {
  const { t } = useTranslation('tasks')
  const queryClient = useQueryClient()

  const defaultCamId =
    initialCameraId || (cameras.length > 0 ? cameras[0].id : '')
  const {
    cameraId,
    channelEnabled,
    setChannelEnabled,
    drafts,
    activeAlgoId,
    activeDraft,
    regionEditor,
    changeCamera,
    toggleAlgo,
    selectActiveAlgo,
    updateFps,
    updateConfigJson,
    updateScheduleJson,
    toPayloadDrafts,
  } = useChannelTaskDrafts({
    initialCameraId: defaultCamId,
    initialAlgorithmId,
    algorithms,
    allTasks,
  })

  const [autoEnableDisabled, setAutoEnableDisabled] = useState(true)
  const [isPaused, setIsPaused] = useState(false)
  const [surfaceState, setSurfaceState] = useState<FrameSurfaceState>('loading')

  const syncMutation = useSyncChannelAlgorithms()
  const deleteMutation = useDeleteTask()

  const algoMap = useMemo(
    () => new Map(algorithms.map((algo) => [algo.id, algo])),
    [algorithms]
  )
  const currentExistingTask = allTasks.find(
    (task) => task.cameraId === cameraId
  )
  const currentCamera = cameras.find((camera) => camera.id === cameraId)
  const isCameraOnline = currentCamera?.health === 'online'

  // 区域可编辑 = 已勾选算法（有编辑对象）且画面已出首帧（有参考画面）。
  // 二者缺一都必须只读，否则会产出没有参考意义的归一化坐标。
  const showEditor = Boolean(activeDraft?.selected)
  const canEditRegion = showEditor && surfaceState === 'live'
  // 裁剪窗口属于任务（= 相机），不属于某个算法：只要画面出首帧就能编辑，
  // 即使一个算法都没勾选也一样。保存仍要求至少一个算法（否则就是删任务）。
  const canEditCrop = Boolean(cameraId) && surfaceState === 'live'
  const hasCrop = regionEditor.crop !== null

  const points = useMemo(() => {
    const thisCameraItems: SchedulableItem[] = []
    let thisCameraCurrent = 0
    for (const [algoId, draft] of Object.entries(drafts)) {
      if (!draft.selected || !draft.enabled) continue
      const cost = algoMap.get(algoId)?.computeCost ?? DEFAULT_COMPUTE_COST
      const pts = cost * draft.sampleFps
      thisCameraItems.push({
        points: pts,
        schedule: draft.scheduleJson,
      })
      if (isScheduleActiveAt(draft.scheduleJson)) {
        thisCameraCurrent += pts
      }
    }

    const allItems: SchedulableItem[] = [...thisCameraItems]
    let projectedCurrent = thisCameraCurrent
    for (const task of allTasks) {
      if (task.cameraId === cameraId || !task.enabled) continue
      for (const sub of task.algorithms ?? []) {
        if (!sub.enabled) continue
        const cost =
          algoMap.get(sub.algorithmId)?.computeCost ?? DEFAULT_COMPUTE_COST
        const pts = cost * sub.sampleFps
        allItems.push({
          points: pts,
          schedule: sub.scheduleJson,
        })
        if (isScheduleActiveAt(sub.scheduleJson)) {
          projectedCurrent += pts
        }
      }
    }

    const thisCamera = calculatePeakCompute(thisCameraItems)
    const projected = calculatePeakCompute(allItems)
    return {
      thisCamera,
      projected,
      thisCameraCurrent,
      projectedCurrent,
    }
  }, [allTasks, cameraId, drafts, algoMap])

  const isOverBudget = points.projected > MAX_COMPUTE_POINTS
  const selectedDisabledAlgos = useMemo(
    () =>
      Object.entries(drafts)
        .filter(
          ([algoId, draft]) =>
            draft.selected && algoMap.get(algoId)?.status === 'DISABLED'
        )
        .map(([algoId]) => algoId),
    [drafts, algoMap]
  )

  /**
   * 切换相机必须先把画面状态复位成 `loading`：
   * 否则新相机出画前会短暂沿用上一台的 `live`，让区域编辑在错误坐标空间里打开。
   */
  const handleCameraChange = (nextCameraId: string) => {
    setSurfaceState('loading')
    setIsPaused(false)
    changeCamera(nextCameraId)
  }

  const handleSave = () => {
    if (!cameraId) {
      toast.error(t('dialog.cameraPlaceholder', '请选择摄像头'))
      return
    }
    if (isOverBudget) {
      toast.error(t('dialog.budgetWarning', '算力预算超限'))
      return
    }

    for (const [algoId, draft] of Object.entries(drafts)) {
      if (!draft.selected) continue
      const rawJson = draft.configJson?.trim() || '{}'
      let parsed: unknown
      try {
        parsed = JSON.parse(rawJson)
      } catch {
        const algoName = algoMap.get(algoId)?.name || algoId
        toast.error(
          t('dialog.invalidJsonError', {
            name: algoName,
            defaultValue: `算法「${algoName}」的高级参数不是合法的 JSON 格式，请修正后再保存`,
          })
        )
        selectActiveAlgo(algoId)
        return
      }

      // 算法包若定义了 config.schema.json，在前端做预校验阻断，避免后端 400 失败
      const algo = algoMap.get(algoId)
      if (algo?.configSchemaJson) {
        const parsedSchema = parseSchema(algo.configSchemaJson)
        if (parsedSchema && !isZeroConfig(parsedSchema)) {
          const validator = buildConfigFormSchema(parsedSchema)
          const result = validator.safeParse(parsed)
          if (!result.success) {
            const algoName = algo.name || algoId
            const firstIssue = result.error.issues[0]
            const fieldPath = firstIssue?.path?.join('.')
            const issueMsg =
              firstIssue?.message ||
              t('dialog.validationFailed', '参数校验失败')
            const detailMsg = fieldPath
              ? t('dialog.invalidConfigFieldDetail', {
                  field: fieldPath,
                  message: issueMsg,
                  defaultValue: `（字段「${fieldPath}」：${issueMsg}）`,
                })
              : t('dialog.invalidConfigGeneralDetail', {
                  message: issueMsg,
                  defaultValue: `（${issueMsg}）`,
                })
            toast.error(
              t('dialog.invalidConfigError', {
                name: algoName,
                detail: detailMsg,
                defaultValue: `算法「${algoName}」的参数配置不合规${detailMsg}，请检查修正后再保存`,
              })
            )
            selectActiveAlgo(algoId)
            return
          }
        }
      }
    }

    // 画面未就绪时 regionEditable=false：沿用已保存的区域，绝不提交编辑缓冲。
    const payloadDrafts = toPayloadDrafts({ regionEditable: canEditRegion })

    if (payloadDrafts.length === 0) {
      if (currentExistingTask) {
        deleteMutation.mutate(currentExistingTask.id, {
          onSuccess: () => {
            toast.success(
              t('dialog.channelTaskCleared', '已清空并删除该通道的分析任务')
            )
            onClose()
          },
          onError: (err: unknown) => {
            const message =
              err instanceof Error && err.message
                ? err.message
                : t('dialog.deleteFailed', '删除通道任务失败')
            toast.error(message)
          },
        })
      } else {
        toast.error(
          t('dialog.noAlgoSelectedError', '请至少选择一个算法包，或直接取消')
        )
      }
      return
    }

    syncMutation.mutate(
      {
        cameraId,
        enabled: channelEnabled,
        // 整任务 upsert：显式带上当前窗口（null = 全幅），省略会保留旧值。
        crop: regionEditor.serializeCrop(),
        drafts: payloadDrafts,
        existingTasks: allTasks.filter((task) => task.cameraId === cameraId),
      },
      {
        onSuccess: async () => {
          if (autoEnableDisabled && selectedDisabledAlgos.length > 0) {
            try {
              await Promise.all(
                selectedDisabledAlgos.map((algoId) =>
                  algorithmsApi.enableAlgorithm(algoId)
                )
              )
              void queryClient.invalidateQueries({
                queryKey: ALGORITHMS_QUERY_KEY,
              })
              toast.success(
                t(
                  'dialog.saveSuccessWithEnable',
                  '通道算法调度已成功更新，已自动激活相关算法'
                )
              )
            } catch {
              void queryClient.invalidateQueries({
                queryKey: ALGORITHMS_QUERY_KEY,
              })
              toast.warning(
                t(
                  'dialog.saveSuccessEnableFailed',
                  '通道算法调度已更新，但部分算法自动激活失败，请在算法管理中手动启用'
                )
              )
            }
          } else {
            toast.success(t('dialog.saveSuccess', '通道算法调度已成功更新'))
          }
          onClose()
        },
        onError: (err: unknown) => {
          const message =
            err instanceof Error && err.message
              ? err.message
              : t('dialog.saveError', '保存调度配置失败')
          toast.error(message)
        },
      }
    )
  }

  return (
    <DialogContent
      showCloseButton={false}
      className='flex h-[94vh] max-h-[1000px] w-[98vw] max-w-none flex-col gap-0 overflow-hidden border-border bg-background p-0 shadow-2xl sm:max-w-[97vw] 2xl:max-w-[1680px]'
    >
      <ChannelTaskDialogHeader
        camera={{
          cameraId,
          cameras,
          fixedCameraName: initialCameraId ? currentCamera?.name : undefined,
          isCameraOnline,
          onCameraChange: handleCameraChange,
        }}
        budget={{
          channelPoints: points.thisCamera,
          projectedPoints: points.projected,
          channelCurrentPoints: points.thisCameraCurrent,
          projectedCurrentPoints: points.projectedCurrent,
          isOverBudget,
        }}
        switches={{
          channelEnabled,
          onChannelEnabledChange: setChannelEnabled,
          disabledAlgoCount: selectedDisabledAlgos.length,
          autoEnableDisabled,
          onAutoEnableChange: setAutoEnableDisabled,
        }}
        onClose={onClose}
      />

      <div className='flex min-h-0 flex-1 divide-x divide-border overflow-hidden bg-muted/20'>
        <ChannelTaskRegionViewport
          cameraId={cameraId || null}
          activeAlgoName={activeAlgoId ? algoMap.get(activeAlgoId)?.name : ''}
          surfaceState={surfaceState}
          onSurfaceStateChange={setSurfaceState}
          isPaused={isPaused}
          onTogglePause={() => setIsPaused((prev) => !prev)}
          regionEditor={regionEditor}
          showEditor={showEditor}
          canEditRegion={canEditRegion}
          canEditCrop={canEditCrop}
          hasCrop={hasCrop}
          onClearCrop={regionEditor.clearCrop}
        />

        <ChannelTaskAlgoPanel
          algorithms={algorithms}
          drafts={drafts}
          activeAlgoId={activeAlgoId}
          onToggleAlgo={toggleAlgo}
          onSelectAlgo={selectActiveAlgo}
          onUpdateFps={updateFps}
          onUpdateConfigJson={updateConfigJson}
          onUpdateScheduleJson={updateScheduleJson}
          regionEditor={regionEditor}
          canEditRegion={canEditRegion}
        />
      </div>

      <ChannelTaskDialogFooter
        canSave={
          !isOverBudget &&
          !syncMutation.isPending &&
          !deleteMutation.isPending &&
          Boolean(cameraId)
        }
        isSaving={syncMutation.isPending || deleteMutation.isPending}
        onSave={handleSave}
        onClose={onClose}
      />
    </DialogContent>
  )
}

export function ChannelTaskDialog({
  open,
  onOpenChange,
  initialCameraId,
  initialAlgorithmId,
  cameras,
  algorithms,
  allTasks,
}: ChannelTaskDialogProps): React.JSX.Element | null {
  if (!open) return null

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <ChannelTaskDialogContent
        initialCameraId={initialCameraId}
        initialAlgorithmId={initialAlgorithmId}
        cameras={cameras}
        algorithms={algorithms}
        allTasks={allTasks}
        onClose={() => onOpenChange(false)}
      />
    </Dialog>
  )
}
