import {
  AlertCircle,
  Crop,
  MousePointer,
  Pause,
  Play,
  RotateCcw,
  ShieldAlert,
  Sparkles,
  Undo2,
  X,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { STATUS_DOT_CLASS, STATUS_LEVEL_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { MAX_RECTS_PER_CATEGORY } from '../data/region-config'
import { type RegionTool } from './region-drag-types'

export interface RegionEditorToolbarProps {
  tool: RegionTool
  counts: { roi: number; mask: number }
  /** 是否允许修改区域（画面已出首帧 **且** 已选中算法）。 */
  isEditable: boolean
  /** 是否允许编辑裁剪窗口（画面已出首帧即可，不要求已选算法）。 */
  cropEditable: boolean
  /** 是否已有裁剪窗口。 */
  hasCrop: boolean
  /** 画面是否已出首帧。与 `isEditable` 分开：未就绪与未选算法是两种不同的只读原因。 */
  isStreamReady: boolean
  canUndo: boolean
  isPaused: boolean
  onToolChange: (tool: RegionTool) => void
  onUndo: () => void
  onReset: () => void
  /** 清空裁剪窗口。 */
  onCropClear: () => void
  onTogglePause: () => void
}

export function RegionEditorToolbar({
  tool,
  counts,
  isEditable,
  cropEditable,
  hasCrop,
  isStreamReady,
  canUndo,
  isPaused,
  onToolChange,
  onUndo,
  onReset,
  onCropClear,
  onTogglePause,
}: RegionEditorToolbarProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  const requestTool = (next: Exclude<RegionTool, 'select'>) => {
    if (!isEditable) return
    if (next === 'roi' && counts.roi >= MAX_RECTS_PER_CATEGORY) {
      toast.warning(t('region.maxRoiReached', 'ROI 关注区已达上限 (最多 8 个)'))
      return
    }
    if (next === 'mask' && counts.mask >= MAX_RECTS_PER_CATEGORY) {
      toast.warning(
        t('region.maxMaskReached', 'MASK 屏蔽区已达上限 (最多 8 个)')
      )
      return
    }
    onToolChange(next)
  }

  const getDisabledReason = (category: 'roi' | 'mask') => {
    if (!isStreamReady) {
      return t('region.streamNotLive', '画面未就绪（等待视频首帧）')
    }
    if (!isEditable) {
      return t('dialog.selectAlgoToConfigure', '在右侧勾选算法以开始配置')
    }
    const count = category === 'roi' ? counts.roi : counts.mask
    if (count >= MAX_RECTS_PER_CATEGORY) {
      return t('region.maxRoiReached', '已达上限 (最多 8 个)')
    }
    return null
  }

  const renderToolButton = (category: 'roi' | 'mask') => {
    const isRoi = category === 'roi'
    const count = isRoi ? counts.roi : counts.mask
    const isMax = count >= MAX_RECTS_PER_CATEGORY
    const isDisabled = !isEditable || isMax
    const disabledReason = isDisabled ? getDisabledReason(category) : null
    const isActive = tool === category
    const Icon = isRoi ? Sparkles : ShieldAlert

    const button = (
      <Button
        type='button'
        variant='ghost'
        size='sm'
        disabled={isDisabled}
        className={cn(
          'h-7.5 gap-1.5 text-xs font-medium transition-all',
          isActive
            ? isRoi
              ? 'bg-emerald-500/15 font-semibold text-emerald-700 shadow-xs ring-1 ring-emerald-500/30 dark:text-emerald-300'
              : 'bg-violet-500/15 font-semibold text-violet-700 shadow-xs ring-1 ring-violet-500/30 dark:text-violet-300'
            : isRoi
              ? 'text-muted-foreground hover:text-emerald-600 dark:hover:text-emerald-400'
              : 'text-muted-foreground hover:text-violet-600 dark:hover:text-violet-400'
        )}
        onClick={() => requestTool(category)}
      >
        <Icon
          className={cn(
            'size-3.5',
            isActive
              ? isRoi
                ? 'text-emerald-600 dark:text-emerald-400'
                : 'text-violet-600 dark:text-violet-400'
              : 'text-muted-foreground'
          )}
        />
        <span>
          {isRoi
            ? t('region.toolRoi', '+ ROI 关注区')
            : t('region.toolMask', '+ MASK 屏蔽区')}{' '}
          ({count}/{MAX_RECTS_PER_CATEGORY})
        </span>
      </Button>
    )

    if (disabledReason) {
      return (
        <Tooltip>
          <TooltipTrigger asChild>
            <span className='inline-flex'>{button}</span>
          </TooltipTrigger>
          <TooltipContent side='bottom' className='text-xs'>
            {disabledReason}
          </TooltipContent>
        </Tooltip>
      )
    }

    return button
  }

  return (
    <div className='flex items-center justify-between gap-2 border-b border-border bg-card px-5 py-2'>
      <div className='flex items-center rounded-lg border border-border bg-background p-0.5 shadow-xs'>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          className={cn(
            'h-7.5 gap-1.5 text-xs font-medium transition-all',
            tool === 'select'
              ? 'bg-muted font-semibold text-foreground shadow-xs'
              : 'text-muted-foreground hover:text-foreground'
          )}
          onClick={() => onToolChange('select')}
        >
          <MousePointer className='size-3.5' />
          <span>{t('region.toolSelect', '选择 / 调整')}</span>
        </Button>

        {renderToolButton('roi')}
        {renderToolButton('mask')}

        <Button
          type='button'
          variant='ghost'
          size='sm'
          disabled={!cropEditable}
          className={cn(
            'h-7.5 gap-1.5 text-xs font-medium transition-all',
            tool === 'crop'
              ? 'bg-cyan-500/15 font-semibold text-cyan-700 shadow-xs ring-1 ring-cyan-500/30 dark:text-cyan-300'
              : 'text-muted-foreground hover:text-cyan-600 dark:hover:text-cyan-400'
          )}
          title={
            cropEditable
              ? t('region.cropHint', '在画面上拖拽设置任务级裁剪窗口')
              : t('region.streamNotLive', '画面未就绪（等待视频首帧）')
          }
          onClick={() => onToolChange(tool === 'crop' ? 'select' : 'crop')}
        >
          <Crop className='size-3.5' />
          <span>{t('region.toolCrop', '裁剪窗口')}</span>
        </Button>
      </div>

      <div className='flex items-center gap-2'>
        {!isStreamReady && (
          <Badge
            variant='outline'
            className={cn(
              'gap-1.5 px-2 py-0.5 text-xs font-medium',
              STATUS_LEVEL_CLASS.warning
            )}
          >
            <span
              className={cn('size-1.5 rounded-full', STATUS_DOT_CLASS.warning)}
            />
            <AlertCircle className='size-3.5' />
            {t('region.streamNotLive', '画面未就绪（只读浏览）')}
          </Badge>
        )}

        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={!isEditable}
          className={cn(
            'h-8 gap-1.5 text-xs',
            isPaused && STATUS_LEVEL_CLASS.warning
          )}
          title={
            isPaused
              ? t('region.resumeHint', '恢复实时画面')
              : t('region.freezeHint', '冻结当前画面以便精细绘制')
          }
          onClick={onTogglePause}
        >
          {isPaused ? (
            <Play className='size-3.5' />
          ) : (
            <Pause className='size-3.5' />
          )}
          <span>
            {isPaused
              ? t('region.resume', '恢复播放')
              : t('region.freeze', '冻结画面')}
          </span>
        </Button>

        <Separator orientation='vertical' className='h-4' />

        <Button
          type='button'
          variant='ghost'
          size='sm'
          // 撤销范围覆盖 ROI/MASK 与 crop：只要任一可编辑就必须可用。
          disabled={(!isEditable && !cropEditable) || !canUndo}
          className='h-8 gap-1.5 text-xs'
          title={t('region.undoHint', '撤销上一步操作')}
          onClick={onUndo}
        >
          <Undo2 className='size-3.5' />
          <span>{t('region.undo', '撤销')}</span>
        </Button>

        <Button
          type='button'
          variant='ghost'
          size='sm'
          disabled={!isEditable}
          className='h-8 gap-1.5 text-xs text-muted-foreground hover:text-foreground'
          title={t('region.resetHint', '清空当前算法的全部区域')}
          onClick={onReset}
        >
          <RotateCcw className='size-3.5' />
          <span>{t('region.reset', '重置')}</span>
        </Button>

        <Button
          type='button'
          variant='ghost'
          size='sm'
          disabled={!cropEditable || !hasCrop}
          className='h-8 gap-1.5 text-xs text-muted-foreground hover:text-cyan-600 dark:hover:text-cyan-400'
          title={t('region.cropClearHint', '清除任务级裁剪窗口（恢复全幅）')}
          onClick={onCropClear}
        >
          <X className='size-3.5' />
          <span>{t('region.cropClear', '清除裁剪')}</span>
        </Button>
      </div>
    </div>
  )
}
