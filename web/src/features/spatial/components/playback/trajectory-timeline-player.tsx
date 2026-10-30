import {
  Camera,
  ChevronLeft,
  ChevronRight,
  Clock,
  Pause,
  Play,
  X,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { getMediaUrl } from '@/lib/media-url'
import { Button } from '@/components/ui/button'
import { Slider } from '@/components/ui/slider'
import { type TrajectoryNodeInfo } from '../../data/schema'
import { formatClock } from '../../utils/format'

interface TrajectoryTimelinePlayerProps {
  nodes: TrajectoryNodeInfo[]
  currentNodeIndex: number
  isPlaying: boolean
  speed: number
  onNodeIndexChange: (index: number) => void
  onPlayingChange: (playing: boolean) => void
  onSpeedChange: (speed: number) => void
  onClose?: () => void
}

/** 播放倍速循环序列：点击倍速按钮时跳到下一档。 */
const PLAYBACK_SPEEDS = [1, 2, 4]

export function TrajectoryTimelinePlayer({
  nodes,
  currentNodeIndex,
  isPlaying,
  speed,
  onNodeIndexChange,
  onPlayingChange,
  onSpeedChange,
  onClose,
}: TrajectoryTimelinePlayerProps) {
  const { t } = useTranslation('spatial')
  const currentNode = nodes[currentNodeIndex] ?? nodes[0]
  const snapshotUrl = getMediaUrl(currentNode?.snapshotPath)

  const cycleSpeed = () => {
    // 未知倍速（不应出现）按 0 处理，即从第一档重新开始。
    const currentIndex = PLAYBACK_SPEEDS.indexOf(speed)
    const nextIndex = (currentIndex + 1) % PLAYBACK_SPEEDS.length
    onSpeedChange(PLAYBACK_SPEEDS[nextIndex])
  }

  return (
    <div className='absolute start-1/2 bottom-4 z-30 w-11/12 max-w-2xl -translate-x-1/2 space-y-3 rounded-xl border border-border bg-background/95 p-3 shadow-2xl backdrop-blur-md'>
      {currentNode && (
        <div className='flex items-center justify-between gap-3 rounded-lg bg-muted/40 p-2'>
          <div className='flex min-w-0 items-center gap-3'>
            {snapshotUrl ? (
              <img
                src={snapshotUrl}
                alt={t('trajectory.snapshot')}
                className='h-12 w-16 shrink-0 rounded border border-border bg-black/20 object-cover'
              />
            ) : (
              <div className='flex h-12 w-16 shrink-0 items-center justify-center rounded bg-muted text-muted-foreground'>
                <Camera className='h-5 w-5' />
              </div>
            )}

            <div className='min-w-0 space-y-0.5 text-xs'>
              <div className='flex items-center gap-1.5 text-sm font-semibold'>
                <span className='flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-primary text-xs text-primary-foreground'>
                  {currentNode.seq}
                </span>
                <span className='truncate'>{currentNode.cameraName}</span>
              </div>
              <div className='flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-muted-foreground'>
                <span className='flex items-center gap-1'>
                  <Clock className='h-3 w-3' />
                  {formatClock(currentNode.firstSeenAt)}
                </span>
                {currentNode.dwellSec > 0 && (
                  <span className='font-medium text-foreground'>
                    {t('trajectory.dwell', { sec: currentNode.dwellSec })}
                  </span>
                )}
                {currentNode.details && (
                  <span className='font-mono font-bold text-foreground'>
                    {currentNode.details}
                  </span>
                )}
              </div>
            </div>
          </div>

          <div
            className='shrink-0 text-end font-mono text-xs text-muted-foreground'
            aria-label={t('trajectory.progress', {
              current: currentNodeIndex + 1,
              total: nodes.length,
            })}
          >
            <span className='text-sm font-bold text-foreground'>
              {currentNodeIndex + 1}
            </span>{' '}
            / {nodes.length}
          </div>
        </div>
      )}

      <div className='flex items-center gap-3'>
        <div className='flex items-center gap-1'>
          <Button
            variant='ghost'
            size='icon'
            className='h-8 w-8'
            onClick={() => onNodeIndexChange(Math.max(0, currentNodeIndex - 1))}
            disabled={currentNodeIndex === 0}
            aria-label={t('trajectory.previous')}
            title={t('trajectory.previous')}
          >
            <ChevronLeft className='h-4 w-4' />
          </Button>

          <Button
            variant='default'
            size='icon'
            className='h-9 w-9 rounded-full shadow-md'
            onClick={() => onPlayingChange(!isPlaying)}
            aria-label={
              isPlaying ? t('trajectory.pause') : t('trajectory.play')
            }
            title={isPlaying ? t('trajectory.pause') : t('trajectory.play')}
          >
            {isPlaying ? (
              <Pause className='h-4 w-4' />
            ) : (
              <Play className='ms-0.5 h-4 w-4' />
            )}
          </Button>

          <Button
            variant='ghost'
            size='icon'
            className='h-8 w-8'
            onClick={() =>
              onNodeIndexChange(
                Math.min(nodes.length - 1, currentNodeIndex + 1)
              )
            }
            disabled={currentNodeIndex === nodes.length - 1}
            aria-label={t('trajectory.next')}
            title={t('trajectory.next')}
          >
            <ChevronRight className='h-4 w-4' />
          </Button>
        </div>

        <div className='flex-1 px-2'>
          <Slider
            value={[currentNodeIndex]}
            min={0}
            max={Math.max(0, nodes.length - 1)}
            step={1}
            aria-label={t('trajectory.progress', {
              current: currentNodeIndex + 1,
              total: nodes.length,
            })}
            onValueChange={([val]) => {
              if (val !== undefined) onNodeIndexChange(val)
            }}
          />
        </div>

        <Button
          variant='outline'
          size='sm'
          className='h-8 px-2 font-mono text-xs'
          onClick={cycleSpeed}
          aria-label={t('trajectory.speed')}
          title={t('trajectory.speed')}
        >
          {speed}x
        </Button>

        {onClose && (
          <Button
            variant='ghost'
            size='icon'
            className='h-8 w-8 text-muted-foreground'
            onClick={onClose}
            aria-label={t('trajectory.closePlayer')}
            title={t('trajectory.closePlayer')}
          >
            <X className='h-4 w-4' />
          </Button>
        )}
      </div>
    </div>
  )
}
