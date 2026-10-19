import {
  AlertCircle,
  MousePointer,
  Pause,
  Play,
  RotateCcw,
  Slash,
  Square,
  Undo2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { categoryTextClass } from '@/lib/category-palette'
import { statusTextClass } from '@/lib/status-palette'
import { Button } from '@/components/ui/button'
import {
  MAX_RECTS_PER_CATEGORY,
  REGION_CATEGORY_HUE,
} from '../data/region-config'
import { type RegionTool } from './region-drag-types'

export interface RegionEditorToolbarProps {
  tool: RegionTool
  counts: { roi: number; mask: number }
  /** 画面已就绪，允许修改区域。 */
  isEditable: boolean
  canUndo: boolean
  isPaused: boolean
  onToolChange: (tool: RegionTool) => void
  onUndo: () => void
  onReset: () => void
  onTogglePause: () => void
}

export function RegionEditorToolbar({
  tool,
  counts,
  isEditable,
  canUndo,
  isPaused,
  onToolChange,
  onUndo,
  onReset,
  onTogglePause,
}: RegionEditorToolbarProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  const requestTool = (next: Exclude<RegionTool, 'select'>) => {
    if (!isEditable) return
    if (next === 'roi' && counts.roi >= MAX_RECTS_PER_CATEGORY) {
      toast.warning(t('region.maxRoiReached'))
      return
    }
    if (next === 'mask' && counts.mask >= MAX_RECTS_PER_CATEGORY) {
      toast.warning(t('region.maxMaskReached'))
      return
    }
    onToolChange(next)
  }

  return (
    <div className='flex items-center justify-between border-b bg-muted/30 px-6 py-2'>
      <div className='flex items-center gap-1.5'>
        <Button
          type='button'
          variant={tool === 'select' ? 'default' : 'outline'}
          size='sm'
          className='h-8 gap-1.5 text-xs'
          onClick={() => onToolChange('select')}
        >
          <MousePointer className='h-3.5 w-3.5' />
          {t('region.toolSelect')}
        </Button>

        <Button
          type='button'
          variant={tool === 'roi' ? 'default' : 'outline'}
          size='sm'
          disabled={!isEditable || counts.roi >= MAX_RECTS_PER_CATEGORY}
          className={`h-8 gap-1.5 text-xs ${categoryTextClass(REGION_CATEGORY_HUE.roi)}`}
          onClick={() => requestTool('roi')}
        >
          <Square className='h-3.5 w-3.5' />
          {t('region.toolRoi')} ({counts.roi}/{MAX_RECTS_PER_CATEGORY})
        </Button>

        <Button
          type='button'
          variant={tool === 'mask' ? 'default' : 'outline'}
          size='sm'
          disabled={!isEditable || counts.mask >= MAX_RECTS_PER_CATEGORY}
          className={`h-8 gap-1.5 text-xs ${categoryTextClass(REGION_CATEGORY_HUE.mask)}`}
          onClick={() => requestTool('mask')}
        >
          <Slash className='h-3.5 w-3.5' />
          {t('region.toolMask')} ({counts.mask}/{MAX_RECTS_PER_CATEGORY})
        </Button>
      </div>

      <div className='flex items-center gap-2'>
        {!isEditable && (
          <span
            className={`flex items-center gap-1 text-xs ${statusTextClass('warning')}`}
          >
            <AlertCircle className='h-3.5 w-3.5' />
            {t('region.streamNotLive')}
          </span>
        )}

        <Button
          type='button'
          variant='ghost'
          size='sm'
          disabled={!isEditable}
          className='h-8 gap-1.5 text-xs'
          title={isPaused ? t('region.resumeHint') : t('region.freezeHint')}
          onClick={onTogglePause}
        >
          {isPaused ? (
            <Play className='h-3.5 w-3.5' />
          ) : (
            <Pause className='h-3.5 w-3.5' />
          )}
          {isPaused ? t('region.resume') : t('region.freeze')}
        </Button>

        <Button
          type='button'
          variant='ghost'
          size='sm'
          disabled={!canUndo}
          className='h-8 gap-1.5 text-xs'
          title={t('region.undoHint')}
          onClick={onUndo}
        >
          <Undo2 className='h-3.5 w-3.5' />
          {t('region.undo')}
        </Button>

        <Button
          type='button'
          variant='ghost'
          size='sm'
          className='h-8 gap-1.5 text-xs'
          onClick={onReset}
        >
          <RotateCcw className='h-3.5 w-3.5' />
          {t('region.reset')}
        </Button>
      </div>
    </div>
  )
}
