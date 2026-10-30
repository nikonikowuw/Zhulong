import { AlertTriangle, Layers, Sliders } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { STATUS_LEVEL_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { type Algorithm } from '@/features/algorithms/data/schema'
import { MAX_RECTS_PER_CATEGORY } from '../data/region-config'
import {
  DEFAULT_COMPUTE_COST,
  MAX_SAMPLE_FPS,
  MIN_SAMPLE_FPS,
  type AlgoDraftItem,
} from '../hooks/use-channel-task-drafts'
import { type RegionEditorController } from '../hooks/use-region-editor'
import { AlgoConfigDynamicForm } from './algo-config-dynamic-form'
import { FpsStepper } from './fps-stepper'
import { RegionRectList } from './region-rect-list'

export interface ChannelTaskAlgoPanelProps {
  algorithms: Algorithm[]
  drafts: Record<string, AlgoDraftItem>
  activeAlgoId: string | null
  onToggleAlgo: (algoId: string, checked: boolean) => void
  onSelectAlgo: (algoId: string) => void
  onUpdateFps: (algoId: string, sampleFps: number) => void
  onUpdateConfigJson: (algoId: string, configJson: string) => void
  regionEditor: RegionEditorController
  canEditRegion: boolean
}

/** 右检视器：算法调度矩阵 + 当前激活算法的深度配置。 */
export function ChannelTaskAlgoPanel({
  algorithms,
  drafts,
  activeAlgoId,
  onToggleAlgo,
  onSelectAlgo,
  onUpdateFps,
  onUpdateConfigJson,
  regionEditor,
  canEditRegion,
}: ChannelTaskAlgoPanelProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  const activeAlgo = activeAlgoId
    ? (algorithms.find((algo) => algo.id === activeAlgoId) ?? null)
    : null
  const activeDraft = activeAlgoId ? (drafts[activeAlgoId] ?? null) : null
  const selectedCount = Object.values(drafts).filter(
    (draft) => draft.selected
  ).length
  const costPerFrame = activeAlgo?.computeCost ?? DEFAULT_COMPUTE_COST

  return (
    <div className='flex w-[460px] shrink-0 flex-col gap-3.5 overflow-y-auto bg-muted/30 p-4 xl:w-[490px]'>
      {/* 算法调度矩阵 */}
      <div className='space-y-2.5 rounded-xl border border-border bg-card p-4 shadow-xs'>
        <div className='flex items-center justify-between'>
          <div className='flex items-center gap-1.5'>
            <Layers className='size-3.5 text-muted-foreground' />
            <Label className='text-xs font-semibold text-foreground'>
              {t('dialog.algorithmsList', '算法调度矩阵')}
            </Label>
          </div>
          <span className='font-mono text-[11px] text-muted-foreground'>
            {t('dialog.enabledCount', '{{selected}} / {{total}} 已启用', {
              selected: selectedCount,
              total: algorithms.length,
            })}
          </span>
        </div>

        {algorithms.length === 0 ? (
          <div className='rounded-lg border border-dashed border-border p-6 text-center text-xs text-muted-foreground'>
            {t('dialog.noInstalledAlgorithms', '暂无已安装的算法包')}
          </div>
        ) : (
          <div className='flex flex-wrap gap-2'>
            {algorithms.map((algo) => {
              const draft = drafts[algo.id]
              const isSelected = Boolean(draft?.selected)
              const isActive = activeAlgoId === algo.id
              const points =
                (algo.computeCost ?? DEFAULT_COMPUTE_COST) *
                (draft?.sampleFps ?? 5)

              return (
                <div
                  key={algo.id}
                  role='button'
                  tabIndex={0}
                  aria-pressed={isActive}
                  className={cn(
                    'group flex cursor-pointer items-center gap-2 rounded-lg border px-3 py-2 text-xs transition-all focus-visible:ring-1 focus-visible:ring-ring focus-visible:outline-hidden',
                    isActive
                      ? 'border-foreground bg-accent text-accent-foreground shadow-xs ring-1 ring-ring/40'
                      : isSelected
                        ? 'border-border bg-muted/60 text-foreground hover:border-foreground/40'
                        : 'border-dashed border-border/80 bg-background/50 text-muted-foreground hover:border-border hover:bg-muted/40 hover:text-foreground'
                  )}
                  onClick={() => {
                    if (!isSelected) {
                      onToggleAlgo(algo.id, true)
                    } else {
                      onSelectAlgo(algo.id)
                    }
                  }}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter' || event.key === ' ') {
                      event.preventDefault()
                      if (!isSelected) {
                        onToggleAlgo(algo.id, true)
                      } else {
                        onSelectAlgo(algo.id)
                      }
                    }
                  }}
                >
                  <Checkbox
                    id={`algo-chk-${algo.id}`}
                    checked={isSelected}
                    onCheckedChange={(checked) =>
                      onToggleAlgo(algo.id, Boolean(checked))
                    }
                    onClick={(event) => event.stopPropagation()}
                    onKeyDown={(event) => event.stopPropagation()}
                  />
                  <Label
                    htmlFor={`algo-chk-${algo.id}`}
                    className='max-w-[140px] cursor-pointer truncate text-xs font-semibold'
                    onClick={(event) => event.stopPropagation()}
                  >
                    {algo.name}
                  </Label>

                  {isSelected && (
                    <span className='rounded bg-background/80 px-1 py-0.5 font-mono text-[10px] text-muted-foreground shadow-2xs'>
                      {points} {t('channelCard.pointsSuffix', '点')}
                    </span>
                  )}

                  {algo.status === 'DISABLED' && (
                    <Badge
                      variant='outline'
                      className={cn(
                        'h-4 px-1 text-[9px] font-normal',
                        STATUS_LEVEL_CLASS.warning
                      )}
                    >
                      {t('dialog.algoDisabledBadge', '未启用')}
                    </Badge>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </div>

      {/* 当前激活算法的深度检视器 */}
      {activeAlgo && activeDraft?.selected ? (
        <div className='flex flex-1 flex-col gap-3.5'>
          {activeAlgo.status === 'DISABLED' && (
            <div
              className={cn(
                'flex items-start gap-2 rounded-lg border p-3 text-xs',
                STATUS_LEVEL_CLASS.warning
              )}
            >
              <AlertTriangle className='mt-0.5 size-4 shrink-0' />
              <p className='text-[11px] leading-normal'>
                {t(
                  'dialog.algoDisabledAlert',
                  '该算法当前在算法管理中处于禁用状态，任务保存后将处于待命态。'
                )}
              </p>
            </div>
          )}

          {/* 通用采样帧率与算力联动卡片（合并原冗余摘要，精简垂直高度） */}
          <div className='flex items-center justify-between rounded-xl border border-border bg-card px-4 py-3 shadow-xs'>
            <div className='flex flex-col gap-0.5'>
              <div className='flex items-center gap-2'>
                <Label className='text-xs font-semibold text-foreground'>
                  {t('dialog.fpsLabel', '采样帧率')}
                </Label>
                <Badge
                  variant='secondary'
                  className='h-5 px-1.5 font-mono text-[10px] font-semibold text-foreground'
                >
                  {costPerFrame * activeDraft.sampleFps}{' '}
                  {t('channelCard.pointsSuffix', '点')}
                </Badge>
                <span className='font-mono text-[10px] text-muted-foreground'>
                  ({costPerFrame} 点/帧)
                </span>
              </div>
              <span className='font-mono text-[10px] text-muted-foreground'>
                {t('dialog.fpsRange', {
                  min: MIN_SAMPLE_FPS,
                  max: MAX_SAMPLE_FPS,
                })}
              </span>
            </div>
            <FpsStepper
              value={activeDraft.sampleFps}
              min={MIN_SAMPLE_FPS}
              max={MAX_SAMPLE_FPS}
              onChange={(fps) => onUpdateFps(activeAlgo.id, fps)}
            />
          </div>

          {/* 算法运行参数表单 */}
          <div className='rounded-xl border border-border bg-card p-4 shadow-xs'>
            <AlgoConfigDynamicForm
              key={activeAlgo.id}
              schemaJson={activeAlgo.configSchemaJson}
              value={activeDraft.configJson}
              onChange={(nextJson) =>
                onUpdateConfigJson(activeAlgo.id, nextJson)
              }
            />
          </div>

          <div className='space-y-3 rounded-xl border border-border bg-card p-4 shadow-xs'>
            <div className='flex items-center justify-between'>
              <div className='flex items-center gap-1.5'>
                <Sliders className='size-3.5 text-muted-foreground' />
                <Label className='text-xs font-semibold text-foreground'>
                  {t('region.entryLabel', '区域过滤策略 (ROI / MASK)')}
                </Label>
              </div>
              <span className='font-mono text-[10px] text-muted-foreground'>
                ROI: {regionEditor.counts.roi}/{MAX_RECTS_PER_CATEGORY} · MASK:{' '}
                {regionEditor.counts.mask}/{MAX_RECTS_PER_CATEGORY}
              </span>
            </div>

            <RegionRectList
              draft={regionEditor.draft}
              selected={regionEditor.selected}
              disabled={!canEditRegion}
              onSelect={regionEditor.select}
              onDelete={regionEditor.deleteRect}
            />
          </div>
        </div>
      ) : (
        <div className='flex flex-1 flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-border bg-card/60 p-8 text-center text-muted-foreground'>
          <div className='flex size-12 items-center justify-center rounded-full bg-muted text-muted-foreground'>
            <Layers className='size-6' />
          </div>
          <p className='text-xs'>
            {t(
              'dialog.selectAlgoToConfigure',
              '在上方勾选算法以开始配置采样率与运行参数'
            )}
          </p>
        </div>
      )}
    </div>
  )
}
