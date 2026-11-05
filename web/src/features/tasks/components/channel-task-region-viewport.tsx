import { Lock, ShieldAlert, Sparkles, Video } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { STATUS_DOT_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { CameraFrameSurface, type FrameSurfaceState } from '@/features/live'
import { emptyDraft } from '../data/region-config'
import { type RegionEditorController } from '../hooks/use-region-editor'
import { RegionCanvas } from './region-canvas'
import { RegionEditorToolbar } from './region-editor-toolbar'

export interface ChannelTaskRegionViewportProps {
  cameraId: string | null
  /** 画面上标注的是哪个算法（用于文案）。 */
  activeAlgoName?: string
  surfaceState: FrameSurfaceState
  onSurfaceStateChange: (state: FrameSurfaceState) => void
  isPaused: boolean
  onTogglePause: () => void
  regionEditor: RegionEditorController
  /** 是否正在为某个已勾选算法编辑区域（决定是否展示叠加层与工具栏）。 */
  showEditor: boolean
  /**
   * 是否允许修改区域坐标。
   *
   * 门禁是「**已渲染首帧**」而不是「已连接」：连接成功后首帧到达前的空画布上绘制，
   * 产出的坐标没有任何参考意义。画面不可用时一律只读（PRD R5 / AC8）。
   */
  canEditRegion: boolean
  /** 是否允许编辑任务级裁剪窗口：只要画面出首帧即可，**不要求已选算法**。 */
  canEditCrop: boolean
  /** 是否已有裁剪窗口（决定“清除裁剪”按钮可用性）。 */
  hasCrop: boolean
  /** 清空裁剪窗口（自带撤销快照）。 */
  onClearCrop: () => void
}

/**
 * 左视口：实时主码流画面 + 区域叠加层 + 绘制工具条。
 *
 * 叠加层**只**通过 {@link CameraFrameSurface} 的 `overlay` 插槽渲染——该组件负责把
 * 容器精确对齐到画面实际绘制区（letterbox 留白不计入坐标空间）。此处不许再套比例容器，
 * 否则归一化坐标会与在线时的坐标空间脱节。
 */
export function ChannelTaskRegionViewport({
  cameraId,
  activeAlgoName,
  surfaceState,
  onSurfaceStateChange,
  isPaused,
  onTogglePause,
  regionEditor,
  showEditor,
  canEditRegion,
  canEditCrop,
  hasCrop,
  onClearCrop,
}: ChannelTaskRegionViewportProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  const handleReset = () => {
    if (!canEditRegion) return
    regionEditor.snapshot()
    regionEditor.changeDraft(emptyDraft())
  }

  return (
    <div className='flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-zinc-950'>
      <RegionEditorToolbar
        tool={regionEditor.tool}
        counts={regionEditor.counts}
        isEditable={canEditRegion}
        cropEditable={canEditCrop}
        hasCrop={hasCrop}
        isStreamReady={surfaceState === 'live'}
        canUndo={regionEditor.canUndo}
        isPaused={isPaused}
        onToolChange={regionEditor.setTool}
        onUndo={regionEditor.undo}
        onReset={handleReset}
        onCropClear={onClearCrop}
        onTogglePause={onTogglePause}
      />

      <div className='relative flex min-h-0 flex-1 items-stretch overflow-hidden bg-black select-none'>
        {cameraId ? (
          <CameraFrameSurface
            cameraId={cameraId}
            paused={isPaused}
            onStateChange={onSurfaceStateChange}
            overlay={() => (
              <RegionCanvas
                tool={regionEditor.tool}
                draft={regionEditor.draft}
                selected={regionEditor.selected}
                // 门禁按工具分流：crop 不依赖「已选算法」，ROI/MASK 依赖。
                disabled={
                  regionEditor.tool === 'crop' ? !canEditCrop : !canEditRegion
                }
                onChange={regionEditor.changeDraft}
                onCreate={regionEditor.createRect}
                onSelect={regionEditor.select}
                onBeforeChange={regionEditor.snapshot}
                crop={regionEditor.crop}
                onCropChange={regionEditor.setCrop}
              />
            )}
          />
        ) : (
          <div className='flex flex-1 flex-col items-center justify-center gap-2 text-muted-foreground'>
            <Video className='size-8 text-muted-foreground/40' />
            <p className='text-xs'>
              {t('dialog.cameraPlaceholder', '请选择摄像头')}
            </p>
          </div>
        )}

        {/* 显式降级态：只读浏览，绝不静默产出错误坐标 */}
        {showEditor && !canEditRegion && (
          <div className='pointer-events-none absolute inset-x-3 bottom-3 rounded-lg border border-border/60 bg-black/80 px-3 py-2 text-white shadow-lg'>
            <div className='flex items-center gap-1.5 text-[11px] font-semibold'>
              {/* 状态指示走 §1.3.3 的 Dot-Indicator 范式，不在视频叠加层上手写色值 */}
              <span
                className={cn(
                  'size-1.5 rounded-full',
                  STATUS_DOT_CLASS.warning
                )}
              />
              <Lock className='size-3.5' />
              {t('dialog.regionLockedTitle', '无参考画面，区域策略暂不可编辑')}
            </div>
            <p className='mt-0.5 text-[11px] leading-normal text-white/70'>
              {surfaceState === 'loading'
                ? t(
                    'dialog.regionLockedLoading',
                    '正在等待主码流首帧…出画后自动解锁绘制。'
                  )
                : t(
                    'dialog.regionLockedDesc',
                    '实时主码流不可用（相机离线或解码失败）。区域坐标必须基于真实画面，因此禁止绘制与保存新坐标；已存区域保持只读展示。'
                  )}
            </p>
          </div>
        )}

        {showEditor && canEditRegion && (
          <div className='pointer-events-none absolute start-3 bottom-3 flex items-center gap-2 rounded-md bg-black/80 px-2.5 py-1 text-xs text-white shadow-md backdrop-blur-sm'>
            <Sparkles className='size-3.5 text-emerald-400' />
            <span>
              {t(
                'dialog.regionDrawingHint',
                '标定模式：在画面上拖拽即可圈定 {{name}} 的生效区域',
                { name: activeAlgoName ?? '' }
              )}
            </span>
          </div>
        )}

        {/*
          没有选中算法时不再用全屏遮罩挡住画布：裁剪窗口属于任务、不属于算法，
          仍然可以查看/编辑。提示改成不拦截指针的浮动条。
        */}
        {!showEditor && (
          <div className='pointer-events-none absolute inset-x-0 top-3 flex justify-center p-1'>
            <div className='flex items-center gap-1.5 rounded-md bg-black/80 px-3 py-1.5 text-xs text-white shadow-md backdrop-blur-sm'>
              <ShieldAlert className='size-3.5 text-amber-400' />
              <span>
                {t(
                  'dialog.selectAlgoToConfigure',
                  '在右侧勾选算法以配置 ROI / MASK；裁剪窗口可独立编辑'
                )}
              </span>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
