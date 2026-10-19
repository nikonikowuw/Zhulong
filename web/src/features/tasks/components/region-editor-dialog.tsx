import { useEffect, useRef, useState } from 'react'
import { HelpCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
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
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { CameraFrameSurface, type FrameSurfaceState } from '@/features/live'
import { useRegionEditor } from '../hooks/use-region-editor'
import { RegionCanvas } from './region-canvas'
import { RegionEditorToolbar } from './region-editor-toolbar'
import { RegionRectList } from './region-rect-list'

export interface RegionEditorDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  cameraId: string
  cameraName?: string
  algorithmName?: string
  value: string
  onSave: (regionConfigJson: string) => void
}

export function RegionEditorDialog({
  open,
  onOpenChange,
  cameraId,
  cameraName,
  algorithmName,
  value,
  onSave,
}: RegionEditorDialogProps): React.JSX.Element {
  const { t } = useTranslation('tasks')
  const editor = useRegionEditor()
  const [surfaceState, setSurfaceState] = useState<FrameSurfaceState>('loading')
  const [isPaused, setIsPaused] = useState(false)

  const isEditable = surfaceState === 'live'
  const { reset } = editor
  const wasOpenRef = useRef(false)

  /**
   * 每次打开都从最新的已存配置重建草稿。
   * 放在 effect 里而不是 `onOpenChange` 回调中——Radix 只在交互导致开合时回调，
   * 若组件以 `open` 初始为 true 挂载，回调根本不会触发，草稿会一直是空的。
   */
  useEffect(() => {
    if (open && !wasOpenRef.current) {
      reset(value)
      setSurfaceState('loading')
      setIsPaused(false)
    }
    wasOpenRef.current = open
  }, [open, value, reset])

  const handleConfirmSave = () => {
    if (!isEditable) {
      toast.error(t('region.cannotSaveWithoutStream'))
      return
    }
    onSave(editor.serialize())
    onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='flex h-[88vh] max-h-[850px] w-[95vw] max-w-5xl flex-col p-0'>
        <DialogHeader className='border-b px-6 pt-5 pb-3'>
          <div className='flex items-center justify-between'>
            <div className='flex items-center gap-2'>
              <DialogTitle className='text-base font-semibold'>
                {t('region.dialogTitle')}
              </DialogTitle>
              {algorithmName && (
                <span className='text-xs text-muted-foreground'>
                  [{algorithmName}]
                </span>
              )}
              {cameraName && (
                <span className='text-xs text-muted-foreground/80'>
                  - {cameraName}
                </span>
              )}
            </div>

            <TooltipProvider>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant='ghost'
                    size='icon'
                    className='h-7 w-7 text-muted-foreground'
                  >
                    <HelpCircle className='h-4 w-4' />
                  </Button>
                </TooltipTrigger>
                <TooltipContent
                  side='left'
                  className='max-w-xs space-y-1 text-xs'
                >
                  <p className='font-semibold'>{t('region.ruleTitle')}:</p>
                  <p>{t('region.rule1')}</p>
                  <p>{t('region.rule2')}</p>
                  <p>{t('region.rule3')}</p>
                </TooltipContent>
              </Tooltip>
            </TooltipProvider>
          </div>
          <DialogDescription className='text-xs text-muted-foreground'>
            {t('region.dialogDesc')}
          </DialogDescription>
        </DialogHeader>

        <RegionEditorToolbar
          tool={editor.tool}
          counts={editor.counts}
          isEditable={isEditable}
          canUndo={editor.canUndo}
          isPaused={isPaused}
          onToolChange={editor.setTool}
          onUndo={editor.undo}
          onReset={() => editor.reset(value)}
          onTogglePause={() => setIsPaused((prev) => !prev)}
        />

        {/* 左侧实时画面 + 叠加绘制层，右侧区域清单 */}
        <div className='flex min-h-0 flex-1 divide-x overflow-hidden'>
          <div className='relative flex flex-1 items-center justify-center overflow-hidden bg-black'>
            <CameraFrameSurface
              cameraId={cameraId}
              paused={isPaused}
              onStateChange={setSurfaceState}
              overlay={() => (
                <RegionCanvas
                  tool={editor.tool}
                  draft={editor.draft}
                  selected={editor.selected}
                  disabled={!isEditable}
                  onChange={editor.changeDraft}
                  onCreate={editor.createRect}
                  onSelect={editor.select}
                  onBeforeChange={editor.snapshot}
                />
              )}
            />
          </div>

          <div className='flex w-80 flex-col divide-y overflow-y-auto bg-card p-4'>
            <RegionRectList
              draft={editor.draft}
              selected={editor.selected}
              disabled={!isEditable}
              onSelect={editor.select}
              onDelete={editor.deleteRect}
            />
          </div>
        </div>

        <DialogFooter className='items-center justify-between border-t bg-muted/20 px-6 py-3 sm:justify-between'>
          <div className='text-xs text-muted-foreground'>
            {t('region.summaryTip', {
              roi: editor.counts.roi,
              mask: editor.counts.mask,
            })}
          </div>

          <div className='flex items-center gap-2'>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() => onOpenChange(false)}
            >
              {t('region.cancel')}
            </Button>
            <Button
              type='button'
              size='sm'
              disabled={!isEditable}
              onClick={handleConfirmSave}
            >
              {t('region.save')}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
