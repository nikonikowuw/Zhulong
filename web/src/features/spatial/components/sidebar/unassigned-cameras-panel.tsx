import { useMemo, useState } from 'react'
import {
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  Eye,
  EyeOff,
  GripVertical,
  Search,
  Video,
  X,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { type Camera } from '@/features/cameras/contract'
import { CameraFrameSurface } from '@/features/live/contract'
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
  const [isCollapsed, setIsCollapsed] = useState(false)
  const [previewCameraId, setPreviewCameraId] = useState<string | null>(null)

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

  const togglePreview = (cameraId: string) => {
    setPreviewCameraId((current) => (current === cameraId ? null : cameraId))
  }

  if (isCollapsed) {
    return (
      <div className='flex h-full w-12 shrink-0 flex-col items-center justify-between rounded-xl border border-border bg-card py-3 shadow-xs'>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant='ghost'
              size='icon'
              className='h-8 w-8 text-muted-foreground hover:text-foreground'
              onClick={() => setIsCollapsed(false)}
            >
              <ChevronRight className='h-4 w-4' />
            </Button>
          </TooltipTrigger>
          <TooltipContent side='right'>
            {t('workbench.togglePanel')}
          </TooltipContent>
        </Tooltip>

        <div className='flex flex-col items-center gap-2'>
          <div className='rounded-full bg-muted p-2 text-muted-foreground'>
            <Video className='h-4 w-4' />
          </div>
          <Badge
            variant='secondary'
            className='h-5 px-1.5 font-mono text-[10px]'
          >
            {unassigned.length}
          </Badge>
        </div>

        <div className='h-4' />
      </div>
    )
  }

  return (
    <div className='flex h-full w-64 shrink-0 flex-col rounded-xl border border-border bg-card shadow-xs sm:w-72'>
      {/* 侧栏顶栏 */}
      <div className='flex items-center justify-between border-b border-border/80 px-3.5 py-3'>
        <div className='flex items-center gap-2 text-sm font-semibold tracking-tight'>
          <Video className='h-4 w-4 text-primary' />
          <span>{t('calibration.unassignedTitle')}</span>
          <Badge variant='secondary' className='font-mono text-xs'>
            {unassigned.length}
          </Badge>
        </div>
        <Button
          variant='ghost'
          size='icon'
          className='h-7 w-7 text-muted-foreground hover:text-foreground'
          onClick={() => setIsCollapsed(true)}
          title={t('workbench.togglePanel')}
        >
          <ChevronLeft className='h-4 w-4' />
        </Button>
      </div>

      {/* 搜索框 */}
      <div className='p-3 pb-2'>
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

      {/* 提示文案 */}
      <div className='px-3 pb-2'>
        <p className='text-[11px] leading-relaxed text-muted-foreground'>
          {t('calibration.unassignedTip')}
        </p>
      </div>

      {/* 列表区域 */}
      <ScrollArea className='flex-1 px-3 pb-3'>
        <div className='space-y-4'>
          {/* 待标定分组 */}
          <div className='space-y-2'>
            <div className='flex items-center justify-between px-1 text-[11px] font-medium text-muted-foreground'>
              <span>{t('calibration.unassignedSection')}</span>
              <span className='font-mono'>({unassigned.length})</span>
            </div>

            {unassigned.length === 0 ? (
              <p className='rounded-lg border border-dashed border-border py-4 text-center text-xs text-muted-foreground'>
                {t('calibration.allAssigned')}
              </p>
            ) : (
              unassigned.map((camera) => {
                const isSelected = camera.id === selectedCameraId
                const isPreviewOpen =
                  isSelected || previewCameraId === camera.id

                return (
                  <div
                    key={camera.id}
                    draggable
                    onDragStart={(e) => handleDragStart(e, camera)}
                    className={`group rounded-lg border p-2 transition-all ${
                      isSelected
                        ? 'border-primary bg-primary/5 ring-1 ring-primary/30'
                        : 'border-border bg-card hover:border-primary/50 hover:bg-accent/30'
                    }`}
                  >
                    <div className='flex items-center justify-between gap-2'>
                      <div className='flex min-w-0 items-center gap-1.5'>
                        <GripVertical className='h-3.5 w-3.5 shrink-0 cursor-grab text-muted-foreground/50 active:cursor-grabbing' />
                        <span className='truncate text-xs font-medium'>
                          {camera.name}
                        </span>
                      </div>

                      <div className='flex shrink-0 items-center gap-1'>
                        <Button
                          variant='ghost'
                          size='icon'
                          className='h-6 w-6 text-muted-foreground hover:text-foreground'
                          onClick={(e) => {
                            e.stopPropagation()
                            togglePreview(camera.id)
                          }}
                          title={
                            isPreviewOpen
                              ? t('calibration.closeCameraPreview')
                              : t('calibration.cameraPreview')
                          }
                        >
                          {isPreviewOpen ? (
                            <EyeOff className='h-3.5 w-3.5 text-primary' />
                          ) : (
                            <Eye className='h-3.5 w-3.5' />
                          )}
                        </Button>

                        <Button
                          variant={isSelected ? 'default' : 'secondary'}
                          size='sm'
                          className='h-6 px-2 text-[11px]'
                          onClick={() => onSelectCamera(camera)}
                        >
                          {isSelected
                            ? t('calibration.stampingActive')
                            : t('calibration.stampAction')}
                        </Button>
                      </div>
                    </div>

                    {/* 相机实时画面随动展开小窗 */}
                    {isPreviewOpen && (
                      <div className='mt-2 overflow-hidden rounded-md border border-border/80 bg-black/60'>
                        <div className='aspect-video w-full'>
                          <CameraFrameSurface
                            cameraId={camera.id}
                            className='h-full w-full'
                          />
                        </div>
                      </div>
                    )}
                  </div>
                )
              })
            )}
          </div>

          {/* 已标定分组 */}
          {assigned.length > 0 && (
            <div className='space-y-2 border-t border-border/80 pt-3'>
              <div className='flex items-center justify-between px-1 text-[11px] font-medium text-muted-foreground'>
                <span>{t('calibration.assignedSection')}</span>
                <span className='font-mono'>({assigned.length})</span>
              </div>

              {assigned.map((camera) => {
                const position = positionByCameraId.get(camera.id)
                return (
                  <div
                    key={camera.id}
                    className='group flex items-center justify-between gap-2 rounded-lg border border-border/60 bg-muted/20 px-2.5 py-1.5 text-xs'
                  >
                    <div className='flex min-w-0 items-center gap-1.5'>
                      <CheckCircle2 className='h-3.5 w-3.5 shrink-0 text-emerald-500' />
                      <span className='truncate text-xs font-medium'>
                        {camera.name}
                      </span>
                    </div>

                    <div className='flex shrink-0 items-center gap-1.5'>
                      {position && (
                        <span className='font-mono text-[10px] text-muted-foreground'>
                          {(position.x * 100).toFixed(0)}%,{' '}
                          {(position.y * 100).toFixed(0)}%
                        </span>
                      )}
                      <Button
                        variant='ghost'
                        size='icon'
                        className='h-6 w-6 text-muted-foreground hover:text-destructive'
                        onClick={() => onRemovePosition(camera.id)}
                        title={t('calibration.removePoint')}
                      >
                        <X className='h-3.5 w-3.5' />
                      </Button>
                    </div>
                  </div>
                )
              })}
            </div>
          )}
        </div>
      </ScrollArea>
    </div>
  )
}
