import { useState, useRef } from 'react'
import { Video } from 'lucide-react'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import {
  type CameraPositionInfo,
  type TrajectoryNodeInfo,
} from '../../data/schema'
import { CameraLivePopover } from '../preview/camera-live-popover'

interface CameraMarkerProps {
  position: CameraPositionInfo
  isCalibrating: boolean
  trajectorySeq?: number
  /** 该摄像头在本次轨迹查询中的节点（用于浮窗里展示抓拍与时间戳）。 */
  trajectoryNode?: TrajectoryNodeInfo
  /** 播放器当前途经高亮。 */
  isActiveWaypoint?: boolean
  onMove?: (newX: number, newY: number) => void
  onAngleChange?: (newAngle: number) => void
  onRemove?: () => void
}

/** 视场角扇形张角（度）。 */
const FOV_SPREAD_DEG = 60

/**
 * 徽标视觉状态，优先级：播放途经 > 已入轨迹 > 标定中 > 常规。
 */
function markerToneClass(
  isActiveWaypoint: boolean,
  hasTrajectoryNode: boolean,
  isCalibrating: boolean
): string {
  if (isActiveWaypoint) {
    return 'scale-125 border-background bg-primary text-primary-foreground ring-4 ring-primary/40'
  }
  if (hasTrajectoryNode) {
    return 'scale-110 border-background bg-primary text-primary-foreground ring-2 ring-primary/40'
  }
  if (isCalibrating) {
    return 'border-primary bg-card text-primary hover:scale-110 active:scale-95'
  }
  return 'border-border bg-card/90 text-foreground backdrop-blur-sm hover:scale-110'
}

export function CameraMarker({
  position,
  isCalibrating,
  trajectorySeq,
  trajectoryNode,
  isActiveWaypoint,
  onMove,
  onAngleChange,
  onRemove,
}: CameraMarkerProps) {
  const [popoverOpen, setPopoverOpen] = useState(false)
  const isDraggingRef = useRef(false)
  const dragStartRef = useRef({ x: 0, y: 0, initialX: 0, initialY: 0 })

  const hasTrajectoryNode = trajectorySeq !== undefined
  const coneRadius = 40
  const startAngleRad =
    ((position.angle - FOV_SPREAD_DEG / 2 - 90) * Math.PI) / 180
  const endAngleRad =
    ((position.angle + FOV_SPREAD_DEG / 2 - 90) * Math.PI) / 180
  const x1 = coneRadius * Math.cos(startAngleRad)
  const y1 = coneRadius * Math.sin(startAngleRad)
  const x2 = coneRadius * Math.cos(endAngleRad)
  const y2 = coneRadius * Math.sin(endAngleRad)
  const fovPath = `M 0 0 L ${x1} ${y1} A ${coneRadius} ${coneRadius} 0 0 1 ${x2} ${y2} Z`

  const handlePointerDown = (e: React.PointerEvent) => {
    if (!isCalibrating || !onMove) return
    e.stopPropagation()
    e.currentTarget.setPointerCapture(e.pointerId)
    isDraggingRef.current = true
    dragStartRef.current = {
      x: e.clientX,
      y: e.clientY,
      initialX: position.x,
      initialY: position.y,
    }
  }

  const handlePointerMove = (e: React.PointerEvent) => {
    if (!isDraggingRef.current || !onMove) return
    e.stopPropagation()

    // getBoundingClientRect() 已经包含祖先 transform 的缩放：宽高就是屏幕上
    // 的可见尺寸。再除一次 zoomScale 会把位移修正两遍，缩放后点位跟不上鼠标。
    const container = e.currentTarget.closest('[data-spatial-canvas-content]')
    if (!container) return
    const rect = container.getBoundingClientRect()

    const nextX =
      dragStartRef.current.initialX +
      (e.clientX - dragStartRef.current.x) / rect.width
    const nextY =
      dragStartRef.current.initialY +
      (e.clientY - dragStartRef.current.y) / rect.height

    onMove(Math.max(0, Math.min(1, nextX)), Math.max(0, Math.min(1, nextY)))
  }

  const handlePointerUp = (e: React.PointerEvent) => {
    if (!isDraggingRef.current) return
    e.stopPropagation()
    isDraggingRef.current = false
    try {
      e.currentTarget.releasePointerCapture(e.pointerId)
    } catch {
      // 指针可能已被浏览器回收，释放失败不影响拖拽结束
    }
  }

  return (
    <div
      style={{
        left: `${position.x * 100}%`,
        top: `${position.y * 100}%`,
      }}
      className='group absolute z-20 -translate-x-1/2 -translate-y-1/2 cursor-pointer select-none'
    >
      <Popover open={popoverOpen} onOpenChange={setPopoverOpen}>
        <PopoverTrigger asChild>
          <div
            className='relative flex items-center justify-center'
            onPointerDown={handlePointerDown}
            onPointerMove={handlePointerMove}
            onPointerUp={handlePointerUp}
          >
            <svg
              className='pointer-events-none absolute overflow-visible transition-transform'
              style={{ width: 0, height: 0 }}
              aria-hidden='true'
            >
              <path
                d={fovPath}
                className='fill-primary/20 stroke-primary/40 stroke-[1.5]'
              />
            </svg>

            <div
              className={`relative flex h-8 w-8 items-center justify-center rounded-full border-2 shadow-lg transition-transform ${markerToneClass(
                Boolean(isActiveWaypoint),
                hasTrajectoryNode,
                isCalibrating
              )}`}
            >
              {hasTrajectoryNode ? (
                <span className='text-xs leading-none font-bold'>
                  {trajectorySeq}
                </span>
              ) : (
                <Video className='h-4 w-4' />
              )}
            </div>

            <div className='pointer-events-none absolute start-1/2 top-9 -translate-x-1/2 rounded border border-border bg-background/90 px-1.5 py-0.5 text-[10px] font-medium whitespace-nowrap text-foreground opacity-80 shadow-sm backdrop-blur-sm transition-opacity group-hover:opacity-100'>
              {position.cameraName || position.cameraId}
            </div>
          </div>
        </PopoverTrigger>

        <PopoverContent
          side='top'
          align='center'
          sideOffset={8}
          className='w-auto border-0 p-0 shadow-none'
        >
          {popoverOpen && (
            <CameraLivePopover
              position={position}
              isCalibrating={isCalibrating}
              trajectoryNode={trajectoryNode}
              onAngleChange={onAngleChange}
              onRemove={onRemove}
              onClose={() => setPopoverOpen(false)}
            />
          )}
        </PopoverContent>
      </Popover>
    </div>
  )
}
