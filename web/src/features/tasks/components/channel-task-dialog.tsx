import { useMemo, useState } from 'react'
import { AlertCircle, Loader2, Video } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { STATUS_LEVEL_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { type Algorithm } from '@/features/algorithms/data/schema'
import { type Camera } from '@/features/cameras/data/schema'
import { type AlgorithmConfigDraft, type Task } from '../data/schema'
import { useSyncChannelAlgorithms } from '../hooks/use-sync-channel-algorithms'
import { MAX_COMPUTE_POINTS } from '../hooks/use-task-compute-budget'

interface ChannelTaskDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  initialCameraId?: string
  cameras: Camera[]
  algorithms: Algorithm[]
  allTasks: Task[]
}

interface AlgoDraftItem {
  selected: boolean
  sampleFps: number
  enabled: boolean
  configJson: string
}

function computeInitialDrafts(
  targetCamId: string,
  allTasks: Task[],
  algorithms: Algorithm[]
): Record<string, AlgoDraftItem> {
  const cameraTasks = allTasks.filter((t) => t.cameraId === targetCamId)
  const taskMap = new Map(cameraTasks.map((t) => [t.algorithmId, t]))

  const initialDrafts: Record<string, AlgoDraftItem> = {}
  for (const algo of algorithms) {
    const existing = taskMap.get(algo.id)
    if (existing) {
      initialDrafts[algo.id] = {
        selected: true,
        sampleFps: existing.sampleFps,
        enabled: existing.enabled,
        configJson: existing.configJson || '{}',
      }
    } else {
      initialDrafts[algo.id] = {
        selected: false,
        sampleFps: 5,
        enabled: true,
        configJson: '{}',
      }
    }
  }
  return initialDrafts
}

interface ChannelTaskDialogContentProps {
  initialCameraId?: string
  cameras: Camera[]
  algorithms: Algorithm[]
  allTasks: Task[]
  onClose: () => void
}

function ChannelTaskDialogContent({
  initialCameraId,
  cameras,
  algorithms,
  allTasks,
  onClose,
}: ChannelTaskDialogContentProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  const defaultCamId =
    initialCameraId || (cameras.length > 0 ? cameras[0].id : '')

  const [cameraId, setCameraId] = useState<string>(defaultCamId)
  const [drafts, setDrafts] = useState<Record<string, AlgoDraftItem>>(() =>
    computeInitialDrafts(defaultCamId, allTasks, algorithms)
  )

  const syncMutation = useSyncChannelAlgorithms()

  const handleCameraChange = (newCamId: string) => {
    setCameraId(newCamId)
    setDrafts(computeInitialDrafts(newCamId, allTasks, algorithms))
  }

  const algoMap = useMemo(
    () => new Map(algorithms.map((a) => [a.id, a])),
    [algorithms]
  )

  // 计算除当前摄像头外，其他摄像头已占用的总算力点数
  const otherCamerasPoints = useMemo(() => {
    let sum = 0
    for (const task of allTasks) {
      if (task.cameraId !== cameraId && task.enabled) {
        const cost = algoMap.get(task.algorithmId)?.computeCost ?? 10
        sum += cost * task.sampleFps
      }
    }
    return sum
  }, [allTasks, cameraId, algoMap])

  // 计算当前摄像头所选算法的总算力点数
  const thisCameraPoints = useMemo(() => {
    let sum = 0
    for (const [algoId, draft] of Object.entries(drafts)) {
      if (draft.selected && draft.enabled) {
        const cost = algoMap.get(algoId)?.computeCost ?? 10
        sum += cost * draft.sampleFps
      }
    }
    return sum
  }, [drafts, algoMap])

  const projectedTotalPoints = otherCamerasPoints + thisCameraPoints
  const isOverBudget = projectedTotalPoints > MAX_COMPUTE_POINTS
  const projectedPercent = Math.min(
    100,
    Math.round((projectedTotalPoints / MAX_COMPUTE_POINTS) * 100)
  )

  const handleToggleAlgo = (algoId: string, checked: boolean) => {
    setDrafts((prev) => ({
      ...prev,
      [algoId]: {
        ...(prev[algoId] ?? { sampleFps: 5, enabled: true, configJson: '{}' }),
        selected: checked,
      },
    }))
  }

  const handleUpdateFps = (algoId: string, val: string) => {
    const parsed = parseInt(val, 10)
    const validFps = Number.isNaN(parsed)
      ? 1
      : Math.max(1, Math.min(30, parsed))
    setDrafts((prev) => ({
      ...prev,
      [algoId]: {
        ...(prev[algoId] ?? {
          selected: true,
          enabled: true,
          configJson: '{}',
        }),
        sampleFps: validFps,
      },
    }))
  }

  const handleUpdateConfigJson = (algoId: string, json: string) => {
    setDrafts((prev) => ({
      ...prev,
      [algoId]: {
        ...(prev[algoId] ?? { selected: true, sampleFps: 5, enabled: true }),
        configJson: json,
      },
    }))
  }

  const handleFormatJson = (algoId: string) => {
    const raw = drafts[algoId]?.configJson ?? '{}'
    try {
      const parsed = JSON.parse(raw)
      handleUpdateConfigJson(algoId, JSON.stringify(parsed, null, 2))
    } catch {
      toast.error('JSON 语法错误，无法格式化')
    }
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

    const payloadDrafts: AlgorithmConfigDraft[] = []
    for (const [algoId, draft] of Object.entries(drafts)) {
      if (draft.selected) {
        // 校验高级参数 JSON 语法合法性
        if (draft.configJson && draft.configJson.trim() !== '') {
          try {
            JSON.parse(draft.configJson)
          } catch {
            const algoName = algoMap.get(algoId)?.name || algoId
            toast.error(
              t('dialog.invalidJsonError', {
                name: algoName,
                defaultValue: `算法「${algoName}」的高级参数不是合法的 JSON 格式，请修正后再保存`,
              })
            )
            return
          }
        }

        payloadDrafts.push({
          algorithmId: algoId,
          sampleFps: draft.sampleFps,
          enabled: draft.enabled,
          configJson: draft.configJson,
        })
      }
    }

    const existingTasks = allTasks.filter((t) => t.cameraId === cameraId)

    syncMutation.mutate(
      {
        cameraId,
        drafts: payloadDrafts,
        existingTasks,
      },
      {
        onSuccess: () => {
          toast.success(t('dialog.saveSuccess', '通道算法调度已成功更新'))
          onClose()
        },
        onError: (err) => {
          toast.error(err.message || t('dialog.saveError', '保存调度配置失败'))
        },
      }
    )
  }

  return (
    <DialogContent className='flex max-h-[90vh] flex-col p-0 sm:max-w-2xl'>
      <DialogHeader className='px-6 pt-6 pb-2'>
        <DialogTitle>{t('dialog.title', '配置通道算法任务')}</DialogTitle>
        <DialogDescription>
          {t(
            'dialog.desc',
            '为摄像头选择需要并发执行的算法包，独立设置采样帧率与运行参数。'
          )}
        </DialogDescription>
      </DialogHeader>

      <div className='flex-1 space-y-5 overflow-y-auto px-6 py-2'>
        {/* 1. 目标摄像头选择 */}
        <div className='space-y-1.5'>
          <Label className='text-xs font-semibold'>
            {t('dialog.selectCamera', '目标摄像头')} *
          </Label>
          {initialCameraId ? (
            <div className='flex items-center gap-2 rounded-lg border border-border bg-muted/30 px-3 py-2 text-sm font-medium'>
              <Video className='h-4 w-4 text-primary' />
              <span>
                {cameras.find((c) => c.id === initialCameraId)?.name ||
                  initialCameraId}
              </span>
            </div>
          ) : (
            <Select value={cameraId} onValueChange={handleCameraChange}>
              <SelectTrigger className='w-full'>
                <SelectValue
                  placeholder={t('dialog.cameraPlaceholder', '请选择摄像头')}
                />
              </SelectTrigger>
              <SelectContent>
                {cameras.map((c) => (
                  <SelectItem key={c.id} value={c.id}>
                    {c.name} (
                    {c.health === 'online'
                      ? t('channelCard.cameraOnline', '在线')
                      : t('channelCard.cameraOffline', '离线')}
                    )
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </div>

        {/* 2. 算法矩阵多选 */}
        <div className='space-y-2.5'>
          <div className='flex items-center justify-between'>
            <Label className='text-xs font-semibold'>
              {t('dialog.algorithmsList', '算法调度矩阵')}
            </Label>
            <span className='text-xs text-muted-foreground'>
              {t('channelCard.channelPoints', '通道算力消耗')}:{' '}
              <strong className='text-foreground'>{thisCameraPoints}</strong>{' '}
              {t('channelCard.pointsSuffix', '点')}
            </span>
          </div>

          {algorithms.length === 0 ? (
            <div className='rounded-lg border border-dashed border-border p-4 text-center text-xs text-muted-foreground'>
              {t('dialog.noInstalledAlgorithms', '暂无已安装的算法包')}
            </div>
          ) : (
            <div className='space-y-3'>
              {algorithms.map((algo) => {
                const draft = drafts[algo.id] ?? {
                  selected: false,
                  sampleFps: 5,
                  enabled: true,
                  configJson: '{}',
                }
                const cost = algo.computeCost ?? 10
                const singleAlgoPoints = cost * draft.sampleFps

                return (
                  <div
                    key={algo.id}
                    className={cn(
                      'rounded-xl border p-3.5 transition-colors',
                      draft.selected
                        ? 'border-primary/40 bg-muted/30'
                        : 'border-border bg-card'
                    )}
                  >
                    {/* 算法选择项 Header */}
                    <div className='flex items-center justify-between gap-3'>
                      <div className='flex items-center gap-3'>
                        <Checkbox
                          id={`algo-chk-${algo.id}`}
                          checked={draft.selected}
                          onCheckedChange={(checked) =>
                            handleToggleAlgo(algo.id, Boolean(checked))
                          }
                        />
                        <div>
                          <Label
                            htmlFor={`algo-chk-${algo.id}`}
                            className='cursor-pointer text-sm font-medium'
                          >
                            {algo.name}
                          </Label>
                          <span className='ms-2 text-xs text-muted-foreground'>
                            v{algo.version}
                          </span>
                        </div>
                      </div>

                      <div className='flex items-center gap-2'>
                        <Badge
                          variant='outline'
                          className='text-xs font-normal'
                        >
                          {cost} {t('dialog.costPerFrame', '点/帧')}
                        </Badge>
                        {draft.selected && (
                          <Badge
                            variant='outline'
                            className={cn(
                              'text-xs font-normal',
                              STATUS_LEVEL_CLASS.active
                            )}
                          >
                            {singleAlgoPoints}{' '}
                            {t('channelCard.pointsSuffix', '点')}
                          </Badge>
                        )}
                      </div>
                    </div>

                    {/* 展开的参数配置区 */}
                    {draft.selected && (
                      <div className='mt-3 space-y-3 border-t border-border/50 ps-7 pt-3'>
                        <div className='flex items-center gap-4'>
                          <Label className='text-xs whitespace-nowrap text-muted-foreground'>
                            {t('dialog.fpsLabel', '采样帧率')}:
                          </Label>
                          <div className='flex items-center gap-2'>
                            <Input
                              type='number'
                              min={1}
                              max={30}
                              value={draft.sampleFps}
                              onChange={(e) =>
                                handleUpdateFps(algo.id, e.target.value)
                              }
                              className='h-8 w-20 text-center text-xs'
                            />
                            <span className='text-xs text-muted-foreground'>
                              FPS
                            </span>
                          </div>
                        </div>

                        <div className='space-y-1.5'>
                          <div className='flex items-center justify-between'>
                            <Label className='text-xs text-muted-foreground'>
                              {t('dialog.configJsonLabel', '高级参数 (JSON)')}:
                            </Label>
                            <Button
                              type='button'
                              variant='ghost'
                              size='sm'
                              className='h-6 px-1.5 text-[10px]'
                              onClick={() => handleFormatJson(algo.id)}
                            >
                              {t('dialog.formatJson', '格式化 JSON')}
                            </Button>
                          </div>
                          <Textarea
                            rows={2}
                            value={draft.configJson}
                            onChange={(e) =>
                              handleUpdateConfigJson(algo.id, e.target.value)
                            }
                            placeholder='{}'
                            className='font-mono text-xs'
                          />
                        </div>
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </div>
      </div>

      {/* 底部算力预测与操作按钮 */}
      <DialogFooter className='items-center gap-3 border-t border-border bg-muted/20 px-6 py-3.5 sm:justify-between'>
        <div className='flex flex-col gap-1 text-start'>
          <div className='flex items-center gap-2 text-xs'>
            <span className='font-semibold'>
              {t('dialog.globalPointsPreview', '全平台预算占用')}:
            </span>
            <span
              className={cn(
                'font-bold',
                isOverBudget ? 'text-destructive' : 'text-foreground'
              )}
            >
              {projectedTotalPoints} / {MAX_COMPUTE_POINTS}{' '}
              {t('channelCard.pointsSuffix', '点')} ({projectedPercent}%)
            </span>
          </div>
          {isOverBudget && (
            <div className='flex items-center gap-1 text-xs text-destructive'>
              <AlertCircle className='h-3.5 w-3.5' />
              <span>{t('dialog.budgetWarning', '已超出总预算上限！')}</span>
            </div>
          )}
        </div>

        <div className='flex items-center gap-2'>
          <Button type='button' variant='outline' onClick={onClose}>
            {t('dialog.cancel', '取消')}
          </Button>
          <Button
            type='button'
            disabled={isOverBudget || syncMutation.isPending || !cameraId}
            onClick={handleSave}
            className='gap-1.5'
          >
            {syncMutation.isPending && (
              <Loader2 className='h-4 w-4 animate-spin' />
            )}
            {t('dialog.save', '保存并下发调度')}
          </Button>
        </div>
      </DialogFooter>
    </DialogContent>
  )
}

export function ChannelTaskDialog({
  open,
  onOpenChange,
  initialCameraId,
  cameras,
  algorithms,
  allTasks,
}: ChannelTaskDialogProps): React.JSX.Element {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {open && (
        <ChannelTaskDialogContent
          initialCameraId={initialCameraId}
          cameras={cameras}
          algorithms={algorithms}
          allTasks={allTasks}
          onClose={() => onOpenChange(false)}
        />
      )}
    </Dialog>
  )
}
