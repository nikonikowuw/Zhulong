import { useState, useRef } from 'react'
import { Maximize2, Move, ZoomIn, ZoomOut } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  type CanvasMap,
  type CameraPositionInfo,
  type TrajectoryNodeInfo,
} from '../../data/schema'
import { CameraMarker } from './camera-marker'
import { TrajectoryLayer } from './trajectory-layer'

/** 待落点的摄像头：点击左侧列表选中后进入「准星」模式。 */
export interface StampingCamera {
  cameraId: string
  cameraName: string
}

interface SpatialCanvasProps {
  currentMap: CanvasMap | null
  positions: CameraPositionInfo[]
  trajectoryNodes: TrajectoryNodeInfo[]
  activeTrajectoryNodeIndex?: number
  segmentDurationMs?: number
  isPlayingTrajectory?: boolean
  isCalibrating: boolean
  stampingCamera: StampingCamera | null
  onDropCamera: (
    cameraId: string,
    cameraName: string,
    x: number,
    y: number
  ) => void
  onStampCamera: (x: number, y: number) => void
  onMoveCamera: (cameraId: string, x: number, y: number) => void
  onAngleChangeCamera: (cameraId: string, angle: number) => void
  onRemoveCamera: (cameraId: string) => void
}

const MIN_SCALE = 0.3
const MAX_SCALE = 4
/** 滚轮与缩放按钮各自的倍率；两者手感不同，故意分开。 */
const WHEEL_ZOOM_STEP = 1.1
const BUTTON_ZOOM_STEP = 1.2

function clampScale(scale: number): number {
  return Math.max(MIN_SCALE, Math.min(MAX_SCALE, scale))
}

/** 画布光标：准星模式 > 平移中 > 可平移。 */
function cursorClass(isStamping: boolean, isPanning: boolean): string {
  if (isStamping) {
    return 'cursor-crosshair'
  }
  if (isPanning) {
    return 'cursor-grabbing'
  }
  return 'cursor-grab'
}

export function SpatialCanvas({
  currentMap,
  positions,
  trajectoryNodes,
  activeTrajectoryNodeIndex,
  segmentDurationMs,
  isPlayingTrajectory,
  isCalibrating,
  stampingCamera,
  onDropCamera,
  onStampCamera,
  onMoveCamera,
  onAngleChangeCamera,
  onRemoveCamera,
}: SpatialCanvasProps) {
  const { t } = useTranslation('spatial')
  const contentRef = useRef<HTMLDivElement>(null)

  const [scale, setScale] = useState(1)
  const [pan, setPan] = useState({ x: 0, y: 0 })
  const [isPanning, setIsPanning] = useState(false)
  const panStartRef = useRef({ x: 0, y: 0, initialPanX: 0, initialPanY: 0 })

  const mapWidth = currentMap?.width || 1200
  const mapHeight = currentMap?.height || 800

  const handleWheel = (e: React.WheelEvent) => {
    const zoomFactor = e.deltaY < 0 ? WHEEL_ZOOM_STEP : 1 / WHEEL_ZOOM_STEP
    setScale((prev) => clampScale(prev * zoomFactor))
  }

  const handlePointerDown = (e: React.PointerEvent) => {
    // 点位与悬浮工具条自己处理指针事件，画布只接管空白区域。
    if ((e.target as HTMLElement).closest('[data-no-pan]')) return
    if (stampingCamera) return
    setIsPanning(true)
    panStartRef.current = {
      x: e.clientX,
      y: e.clientY,
      initialPanX: pan.x,
      initialPanY: pan.y,
    }
    e.currentTarget.setPointerCapture(e.pointerId)
  }

  const handlePointerMove = (e: React.PointerEvent) => {
    if (!isPanning) return
    setPan({
      x: panStartRef.current.initialPanX + (e.clientX - panStartRef.current.x),
      y: panStartRef.current.initialPanY + (e.clientY - panStartRef.current.y),
    })
  }

  const handlePointerUp = (e: React.PointerEvent) => {
    if (!isPanning) return
    setIsPanning(false)
    try {
      e.currentTarget.releasePointerCapture(e.pointerId)
    } catch {
      // 指针可能已被浏览器回收，释放失败不影响平移结束
    }
  }

  /** 屏幕坐标 → 归一化坐标。rect 已包含祖先 transform 的缩放。 */
  const toNormalized = (event: { clientX: number; clientY: number }) => {
    const container = contentRef.current
    if (!container) return null
    const rect = container.getBoundingClientRect()
    if (rect.width === 0 || rect.height === 0) return null
    return {
      x: Math.max(0, Math.min(1, (event.clientX - rect.left) / rect.width)),
      y: Math.max(0, Math.min(1, (event.clientY - rect.top) / rect.height)),
    }
  }

  // 准星模式：在底图上单击落点（Click & Stamp）
  const handleStampClick = (e: React.MouseEvent) => {
    if (!stampingCamera) return
    const point = toNormalized(e)
    if (!point) return
    e.stopPropagation()
    onStampCamera(point.x, point.y)
  }

  const handleDragOver = (e: React.DragEvent) => {
    if (!isCalibrating) return
    e.preventDefault()
    e.dataTransfer.dropEffect = 'copy'
  }

  const handleDrop = (e: React.DragEvent) => {
    if (!isCalibrating) return
    e.preventDefault()

    const cameraId = e.dataTransfer.getData('application/zhulong-camera-id')
    const cameraName = e.dataTransfer.getData('application/zhulong-camera-name')
    if (!cameraId) return

    const point = toNormalized(e)
    if (!point) return
    onDropCamera(cameraId, cameraName, point.x, point.y)
  }

  const resetView = () => {
    setScale(1)
    setPan({ x: 0, y: 0 })
  }

  const zoomBy = (factor: number) => {
    setScale((prev) => clampScale(prev * factor))
  }

  // 点位徽标显示该摄像头在轨迹中的序号；同一摄像头出现多次时取最后一次。
  const nodeByCameraId = new Map<string, TrajectoryNodeInfo>()
  trajectoryNodes.forEach((node) => {
    nodeByCameraId.set(node.cameraId, node)
  })

  const activeWaypointCameraId =
    activeTrajectoryNodeIndex !== undefined
      ? trajectoryNodes[activeTrajectoryNodeIndex]?.cameraId
      : undefined

  return (
    <div
      onWheel={handleWheel}
      onPointerDown={handlePointerDown}
      onPointerMove={handlePointerMove}
      onPointerUp={handlePointerUp}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
      className={`relative flex h-full w-full items-center justify-center overflow-hidden bg-muted/20 select-none ${cursorClass(
        Boolean(stampingCamera),
        isPanning
      )}`}
    >
      <div
        style={{
          transform: `translate(${pan.x}px, ${pan.y}px) scale(${scale})`,
          transformOrigin: 'center center',
          transition: isPanning ? 'none' : 'transform 0.05s ease-out',
        }}
        className='relative shrink-0'
      >
        {currentMap ? (
          <div
            ref={contentRef}
            data-spatial-canvas-content
            onClick={handleStampClick}
            className='relative overflow-hidden rounded-md bg-black/10 shadow-2xl'
            style={{
              width: mapWidth,
              height: mapHeight,
              maxWidth: '90vw',
              maxHeight: '80vh',
            }}
          >
            <img
              src={currentMap.imageUrl}
              alt={currentMap.name}
              draggable={false}
              className='pointer-events-none h-full w-full object-contain'
            />

            <TrajectoryLayer
              nodes={trajectoryNodes}
              activeNodeIndex={activeTrajectoryNodeIndex}
              width={mapWidth}
              height={mapHeight}
              segmentDurationMs={segmentDurationMs}
              isPlaying={isPlayingTrajectory}
            />

            {positions.map((pos) => {
              const node = nodeByCameraId.get(pos.cameraId)
              return (
                <div key={pos.cameraId} data-no-pan>
                  <CameraMarker
                    position={pos}
                    isCalibrating={isCalibrating}
                    trajectorySeq={node?.seq}
                    trajectoryNode={node}
                    isActiveWaypoint={pos.cameraId === activeWaypointCameraId}
                    onMove={(newX, newY) =>
                      onMoveCamera(pos.cameraId, newX, newY)
                    }
                    onAngleChange={(angle) =>
                      onAngleChangeCamera(pos.cameraId, angle)
                    }
                    onRemove={() => onRemoveCamera(pos.cameraId)}
                  />
                </div>
              )
            })}
          </div>
        ) : (
          <div className='flex flex-col items-center justify-center p-12 text-center text-muted-foreground'>
            <Move className='mb-3 h-12 w-12 stroke-[1.5] opacity-40' />
            <p className='text-sm'>{t('canvas.emptyMapHint')}</p>
          </div>
        )}
      </div>

      <div
        data-no-pan
        className='absolute end-4 bottom-4 z-30 flex items-center gap-1 rounded-lg border border-border bg-background/90 p-1 shadow-md backdrop-blur-sm'
      >
        <Button
          variant='ghost'
          size='icon'
          className='h-7 w-7'
          onClick={() => zoomBy(BUTTON_ZOOM_STEP)}
          aria-label={t('canvas.zoomIn')}
          title={t('canvas.zoomIn')}
        >
          <ZoomIn className='h-3.5 w-3.5' />
        </Button>
        <span className='w-10 px-1 text-center font-mono text-[11px] text-muted-foreground'>
          {Math.round(scale * 100)}%
        </span>
        <Button
          variant='ghost'
          size='icon'
          className='h-7 w-7'
          onClick={() => zoomBy(1 / BUTTON_ZOOM_STEP)}
          aria-label={t('canvas.zoomOut')}
          title={t('canvas.zoomOut')}
        >
          <ZoomOut className='h-3.5 w-3.5' />
        </Button>
        <Button
          variant='ghost'
          size='icon'
          className='h-7 w-7'
          onClick={resetView}
          aria-label={t('canvas.resetView')}
          title={t('canvas.resetView')}
        >
          <Maximize2 className='h-3.5 w-3.5' />
        </Button>
      </div>
    </div>
  )
}
