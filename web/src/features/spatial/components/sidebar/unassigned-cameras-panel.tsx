import { useMemo, useState } from 'react'
import { CheckCircle2, GripVertical, Search, Video, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ScrollArea } from '@/components/ui/scroll-area'
import { type Camera } from '@/features/cameras/contract'
import { type CameraPositionInfo } from '../../data/schema'

interface UnassignedCamerasPanelProps {
  cameras: Camera[]
  positions: CameraPositionInfo[]
  /** 当前进入准星模式的摄像头；点选后到画布上单击落点。 */
  selectedCameraId: string | null
  onSelectCamera: (camera: Camera) => void
  onRemovePosition: (cameraId: string) => void
}

export function UnassignedCamerasPanel({
  cameras,
  positions,
  selectedCameraId,
  onSelectCamera,
  onRemovePosition,
}: UnassignedCamerasPanelProps) {
  const { t } = useTranslation('spatial')
  const [search, setSearch] = useState('')

  const { unassigned, assigned, positionByCameraId } = useMemo(() => {
    const byCameraId = new Map(
      positions.map((position) => [position.cameraId, position])
    )
    const keyword = search.toLowerCase().trim()
    const filtered = keyword
      ? cameras.filter((camera) => camera.name.toLowerCase().includes(keyword))
      : cameras
    return {
      unassigned: filtered.filter((camera) => !byCameraId.has(camera.id)),
      assigned: filtered.filter((camera) => byCameraId.has(camera.id)),
      positionByCameraId: byCameraId,
    }
  }, [cameras, positions, search])

  const handleDragStart = (e: React.DragEvent, camera: Camera) => {
    e.dataTransfer.setData('application/zhulong-camera-id', camera.id)
    e.dataTransfer.setData('application/zhulong-camera-name', camera.name)
    e.dataTransfer.effectAllowed = 'copy'
  }

  return (
    <aside className='z-10 flex h-full w-64 shrink-0 flex-col border-e border-border bg-card shadow-sm'>
      <div className='space-y-2 border-b border-border p-3'>
        <div className='flex items-center justify-between gap-2'>
          <h3 className='text-sm font-semibold'>
            {t('calibration.unassignedTitle')}
          </h3>
          <Badge variant='secondary' className='text-xs'>
            {assigned.length}/{cameras.length}
          </Badge>
        </div>
        <p className='text-xs text-muted-foreground'>
          {t('calibration.unassignedTip')}
        </p>
        <div className='relative'>
          <Search className='absolute start-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground' />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t('calibration.searchPlaceholder')}
            aria-label={t('calibration.searchPlaceholder')}
            className='h-8 ps-8 text-xs'
          />
        </div>
      </div>

      <ScrollArea className='flex-1 p-2'>
        <div className='mb-4 space-y-1.5'>
          <span className='px-2 text-[11px] font-medium tracking-wider text-muted-foreground uppercase'>
            {t('calibration.unassignedSection')} ({unassigned.length})
          </span>
          {unassigned.length === 0 ? (
            <p className='px-2 py-3 text-center text-xs text-muted-foreground italic'>
              {t('calibration.allAssigned')}
            </p>
          ) : (
            unassigned.map((camera) => (
              <div
                key={camera.id}
                draggable
                onDragStart={(e) => handleDragStart(e, camera)}
                className={`group flex cursor-grab items-center justify-between gap-2 rounded-md border p-2 transition-colors hover:bg-muted/60 active:cursor-grabbing ${
                  camera.id === selectedCameraId
                    ? 'border-primary bg-primary/5'
                    : 'border-border bg-background'
                }`}
              >
                <div className='flex min-w-0 items-center gap-2'>
                  <GripVertical className='h-3.5 w-3.5 shrink-0 text-muted-foreground/60' />
                  <Video className='h-3.5 w-3.5 shrink-0 text-primary' />
                  <span className='truncate text-xs font-medium'>
                    {camera.name}
                  </span>
                </div>
                <Button
                  variant='ghost'
                  size='sm'
                  className='h-6 shrink-0 px-1.5 text-[11px] opacity-0 transition-opacity group-hover:opacity-100 focus-visible:opacity-100'
                  onClick={() => onSelectCamera(camera)}
                >
                  {t('calibration.enter')}
                </Button>
              </div>
            ))
          )}
        </div>

        {assigned.length > 0 && (
          <div className='space-y-1.5 border-t border-border pt-2'>
            <span className='px-2 text-[11px] font-medium tracking-wider text-muted-foreground uppercase'>
              {t('calibration.assignedSection')} ({assigned.length})
            </span>
            {assigned.map((camera) => {
              const position = positionByCameraId.get(camera.id)
              return (
                <div
                  key={camera.id}
                  className='group flex items-center justify-between gap-2 rounded-md border border-border bg-muted/20 p-2 transition-colors hover:bg-muted/40'
                >
                  <div className='flex min-w-0 items-center gap-2'>
                    <CheckCircle2 className='h-3.5 w-3.5 shrink-0 text-primary' />
                    <span className='truncate text-xs font-medium'>
                      {camera.name}
                    </span>
                  </div>
                  <div className='flex items-center gap-1'>
                    {position && (
                      <span className='font-mono text-[10px] text-muted-foreground'>
                        {(position.x * 100).toFixed(0)}%,{' '}
                        {(position.y * 100).toFixed(0)}%
                      </span>
                    )}
                    <Button
                      variant='ghost'
                      size='icon'
                      className='h-5 w-5 text-muted-foreground hover:text-destructive'
                      onClick={() => onRemovePosition(camera.id)}
                      aria-label={t('calibration.removeFromMap')}
                      title={t('calibration.removeFromMap')}
                    >
                      <X className='h-3 w-3' />
                    </Button>
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </ScrollArea>
    </aside>
  )
}
