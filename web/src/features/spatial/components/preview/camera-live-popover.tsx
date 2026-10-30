import { Compass, Trash2, Video, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { getMediaUrl } from '@/lib/media-url'
import { Button } from '@/components/ui/button'
import { Slider } from '@/components/ui/slider'
import { CameraFrameSurface } from '@/features/live/contract'
import {
  type CameraPositionInfo,
  type TrajectoryNodeInfo,
} from '../../data/schema'
import { formatClock } from '../../utils/format'

interface CameraLivePopoverProps {
  position: CameraPositionInfo
  isCalibrating: boolean
  /** 该摄像头在本次轨迹中的节点；有值时额外展示抓拍原图与到达/离开时间。 */
  trajectoryNode?: TrajectoryNodeInfo
  onAngleChange?: (angle: number) => void
  onRemove?: () => void
  onClose?: () => void
}

export function CameraLivePopover({
  position,
  isCalibrating,
  trajectoryNode,
  onAngleChange,
  onRemove,
  onClose,
}: CameraLivePopoverProps) {
  const { t } = useTranslation('spatial')
  const snapshotUrl = getMediaUrl(trajectoryNode?.snapshotPath)

  return (
    <div className='z-50 w-72 space-y-3 rounded-lg border border-border bg-popover p-3 text-xs text-popover-foreground shadow-xl'>
      <div className='flex items-center justify-between gap-2'>
        <div className='flex min-w-0 items-center gap-1.5 text-sm font-semibold'>
          <Video className='h-4 w-4 shrink-0 text-primary' />
          <span className='truncate'>
            {position.cameraName || position.cameraId}
          </span>
        </div>
        {onClose && (
          <Button
            variant='ghost'
            size='icon'
            className='h-6 w-6 shrink-0'
            onClick={onClose}
            aria-label={t('common:actions.close')}
          >
            <X className='h-3.5 w-3.5' />
          </Button>
        )}
      </div>

      <div>
        <p className='mb-1 text-[11px] text-muted-foreground'>
          {t('calibration.previewStream')}
        </p>
        <div className='relative aspect-video w-full overflow-hidden rounded bg-black'>
          <CameraFrameSurface
            cameraId={position.cameraId}
            className='h-full w-full'
          />
        </div>
      </div>

      {trajectoryNode && (
        <div className='space-y-1 rounded border border-border bg-muted/30 p-2'>
          <p className='text-[11px] font-medium text-muted-foreground'>
            {t('trajectory.snapshot')}
          </p>
          {snapshotUrl ? (
            <img
              src={snapshotUrl}
              alt={t('trajectory.snapshot')}
              className='h-24 w-full rounded bg-black/20 object-contain'
            />
          ) : (
            <div className='flex h-24 w-full items-center justify-center rounded bg-muted text-muted-foreground'>
              <Video className='h-5 w-5' />
            </div>
          )}
          <div className='flex flex-wrap gap-x-3 gap-y-0.5 text-[11px] text-muted-foreground'>
            <span>
              {t('trajectory.firstSeen', {
                time: formatClock(trajectoryNode.firstSeenAt),
              })}
            </span>
            <span>
              {t('trajectory.lastSeen', {
                time: formatClock(trajectoryNode.lastSeenAt),
              })}
            </span>
            {trajectoryNode.dwellSec > 0 && (
              <span>
                {t('trajectory.dwell', { sec: trajectoryNode.dwellSec })}
              </span>
            )}
          </div>
        </div>
      )}

      <div className='grid grid-cols-2 gap-2 rounded bg-muted/30 p-2 text-[11px] text-muted-foreground'>
        <div>
          <span>{t('calibration.coordinates')}: </span>
          <span className='font-mono font-medium text-foreground'>
            {(position.x * 100).toFixed(1)}%, {(position.y * 100).toFixed(1)}%
          </span>
        </div>
        <div>
          <span>{t('calibration.orientation')}: </span>
          <span className='font-mono font-medium text-foreground'>
            {Math.round(position.angle)}°
          </span>
        </div>
      </div>

      {isCalibrating && (
        <div className='space-y-2 border-t border-border pt-1'>
          <div className='flex items-center justify-between'>
            <span className='flex items-center gap-1 text-muted-foreground'>
              <Compass className='h-3.5 w-3.5' />
              {t('calibration.orientation')}
            </span>
            <span className='font-mono text-xs font-semibold'>
              {Math.round(position.angle)}°
            </span>
          </div>
          <Slider
            value={[position.angle]}
            min={0}
            max={359}
            step={1}
            onValueChange={([val]) => onAngleChange?.(val ?? 0)}
          />

          {onRemove && (
            <Button
              variant='outline'
              size='sm'
              className='mt-1 h-7 w-full text-xs text-destructive hover:bg-destructive/10 hover:text-destructive'
              onClick={onRemove}
            >
              <Trash2 className='me-1 h-3.5 w-3.5' />
              {t('calibration.removeFromMap')}
            </Button>
          )}
        </div>
      )}
    </div>
  )
}
