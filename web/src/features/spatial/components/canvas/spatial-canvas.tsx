import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Loader2,
  Map as MapIcon,
  Maximize2,
  Plus,
  RefreshCw,
  ZoomIn,
  ZoomOut,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  type CameraPositionInfo,
  type CanvasMap,
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
  onUploadMap?: () => void
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

const MIN_SCALE = 0.02
const MAX_SCALE = 6
/** 滚轮与缩放按钮各自的倍率；两者手感不同，故意分开。 */
const WHEEL_ZOOM_STEP = 1.1
const BUTTON_ZOOM_STEP = 1.2
/** 点位反向缩放的分母下限，防止极小缩放比例下点位过度放大重叠。 */
const MARKER_INV_MIN_SCALE = 0.15

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
  onUploadMap,
  onDropCamera,
  onStampCamera,
  onMoveCamera,
  onAngleChangeCamera,
  onRemoveCamera,
}: SpatialCanvasProps) {
  const { t } = useTranslation('spatial')
  const viewportRef = useRef<HTMLDivElement>(null)
  const contentRef = useRef<HTMLDivElement>(null)
  const transformWrapperRef = useRef<HTMLDivElement>(null)

  const [scale, setScale] = useState(1)
  const [pan, setPan] = useState({ x: 0, y: 0 })
  const [isPanning, setIsPanning] = useState(false)
  const [loadedMapKey, setLoadedMapKey] = useState<string | null>(null)
  const [failedMapKey, setFailedMapKey] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  const panStartRef = useRef({ x: 0, y: 0, initialPanX: 0, initialPanY: 0 })
  const panRef = useRef({ x: 0, y: 0 })
  const scaleRef = useRef(1)
  const fittedMapIdRef = useRef<string | null>(null)
  const hasUserTransformedRef = useRef(false)

  useEffect(() => {
    panRef.current = pan
  }, [pan])

  useEffect(() => {
    scaleRef.current = scale
  }, [scale])

  const mapWidth = currentMap?.width || 1200
  const mapHeight = currentMap?.height || 800

  const currentKey = currentMap
    ? `${currentMap.id}-${currentMap.imageUrl}-${reloadKey}`
    : null
  const imageLoading =
    currentKey !== null &&
    loadedMapKey !== currentKey &&
    failedMapKey !== currentKey
  const imageError = currentKey !== null && failedMapKey === currentKey

  // 严格自适应当前底图尺寸并居中，防止固定约束破坏纵横比
  const fitView = useCallback(() => {
    const viewport = viewportRef.current
    if (!viewport || !currentMap) {
      scaleRef.current = 1
      panRef.current = { x: 0, y: 0 }
      fittedMapIdRef.current = null
      setScale(1)
      setPan({ x: 0, y: 0 })
      return
    }

    const vw = viewport.clientWidth
    const vh = viewport.clientHeight
    if (vw <= 0 || vh <= 0) return

    const padding = 32
    const availableW = Math.max(100, vw - padding * 2)
    const availableH = Math.max(100, vh - padding * 2)
    const fitRatio = clampScale(
      Math.min(availableW / mapWidth, availableH / mapHeight, 1.0)
    )

    scaleRef.current = fitRatio
    panRef.current = { x: 0, y: 0 }
    fittedMapIdRef.current = currentMap.id
    hasUserTransformedRef.current = false
    setScale(fitRatio)
    setPan({ x: 0, y: 0 })
  }, [currentMap, mapWidth, mapHeight])

  // 切换底图或用户尚未手动缩放/平移时自适应容器变化（如展开/折叠左侧标定栏）；
  // 一旦用户手动缩放或平移过，则保留其当前视口位置。
  useEffect(() => {
    const el = viewportRef.current
    if (!el) return
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) {
        if (entry.contentRect.width > 0 && entry.contentRect.height > 0) {
          const targetId = currentMap?.id ?? null
          if (
            fittedMapIdRef.current !== targetId ||
            !hasUserTransformedRef.current
          ) {
            fitView()
          }
        }
      }
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [currentMap?.id, fitView])

  const handleWheel = (e: React.WheelEvent) => {
    hasUserTransformedRef.current = true
    const zoomFactor = e.deltaY < 0 ? WHEEL_ZOOM_STEP : 1 / WHEEL_ZOOM_STEP
    setScale((prev) => clampScale(prev * zoomFactor))
  }

  const handlePointerDown = (e: React.PointerEvent) => {
    if (e.button !== 0) return
    // 点位与悬浮工具条自己处理指针事件，画布只接管空白区域。
    if ((e.target as HTMLElement).closest('[data-no-pan]')) return
    if (stampingCamera) return
    setIsPanning(true)
    panStartRef.current = {
      x: e.clientX,
      y: e.clientY,
      initialPanX: panRef.current.x,
      initialPanY: panRef.current.y,
    }
    e.currentTarget.setPointerCapture(e.pointerId)
  }

  const handlePointerMove = (e: React.PointerEvent) => {
    if (!isPanning) return
    hasUserTransformedRef.current = true
    const nextX =
      panStartRef.current.initialPanX + (e.clientX - panStartRef.current.x)
    const nextY =
      panStartRef.current.initialPanY + (e.clientY - panStartRef.current.y)
    panRef.current = { x: nextX, y: nextY }
    // 平移拖拽期间直接写 DOM transform，避免每帧触发 React 重渲染整张画布
    if (transformWrapperRef.current) {
      transformWrapperRef.current.style.transform = `translate(${nextX}px, ${nextY}px) scale(${scaleRef.current})`
    }
  }

  const handlePointerUp = (e: React.PointerEvent) => {
    if (!isPanning) return
    setIsPanning(false)
    setPan(panRef.current)
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

  const zoomBy = (factor: number) => {
    hasUserTransformedRef.current = true
    setScale((prev) => clampScale(prev * factor))
  }

  // 点位徽标显示该摄像头在轨迹中的序号；同一摄像头出现多次时取最后一次。
  const nodeByCameraId = useMemo(() => {
    const map = new Map<string, TrajectoryNodeInfo>()
    trajectoryNodes.forEach((node) => {
      map.set(node.cameraId, node)
    })
    return map
  }, [trajectoryNodes])

  const activeWaypointCameraId =
    activeTrajectoryNodeIndex !== undefined
      ? trajectoryNodes[activeTrajectoryNodeIndex]?.cameraId
      : undefined

  const markerScale = 1 / Math.max(scale, MARKER_INV_MIN_SCALE)

  return (
    <div
      ref={viewportRef}
      onWheel={handleWheel}
      onPointerDown={handlePointerDown}
      onPointerMove={handlePointerMove}
      onPointerUp={handlePointerUp}
      onPointerCancel={handlePointerUp}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
      className={`relative flex h-full w-full items-center justify-center overflow-hidden bg-muted/20 select-none ${cursorClass(
        Boolean(stampingCamera),
        isPanning
      )}`}
    >
      <div
        ref={transformWrapperRef}
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
            }}
          >
            {/* 底图图片 */}
            <img
              key={currentKey ?? 'empty'}
              src={currentMap.imageUrl}
              alt={currentMap.name}
              draggable={false}
              onLoad={() => {
                if (currentKey) setLoadedMapKey(currentKey)
                if (fittedMapIdRef.current !== currentMap.id) {
                  fitView()
                }
              }}
              onError={() => {
                if (currentKey) setFailedMapKey(currentKey)
              }}
              className={`pointer-events-none block h-full w-full object-fill transition-opacity duration-300 ${
                imageLoading ? 'opacity-0' : 'opacity-100'
              }`}
            />

            {/* 底图加载中微光动画 */}
            {imageLoading && !imageError && (
              <div className='absolute inset-0 flex flex-col items-center justify-center bg-card/40 backdrop-blur-xs'>
                <Loader2 className='mb-2 h-8 w-8 animate-spin text-primary' />
                <span className='text-xs font-medium text-foreground'>
                  {t('canvas.loading')}
                </span>
              </div>
            )}

            {/* 底图加载失败重试层 */}
            {imageError && (
              <div className='absolute inset-0 flex flex-col items-center justify-center gap-3 bg-card/90 p-6 text-center backdrop-blur-sm'>
                <p className='text-xs text-destructive'>
                  {t('canvas.imageLoadFailed')}
                </p>
                <Button
                  variant='outline'
                  size='sm'
                  className='h-7 gap-1.5 text-xs'
                  onClick={() => setReloadKey((k) => k + 1)}
                >
                  <RefreshCw className='h-3 w-3' />
                  {t('canvas.retry')}
                </Button>
              </div>
            )}

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
                    markerScale={markerScale}
                    trajectorySeq={node?.seq}
                    trajectoryNode={node}
                    isActiveWaypoint={pos.cameraId === activeWaypointCameraId}
                    onMove={onMoveCamera}
                    onAngleChange={onAngleChangeCamera}
                    onRemove={onRemoveCamera}
                  />
                </div>
              )
            })}
          </div>
        ) : (
          /* 空态引导卡片：没有底图时直接呈现醒目的行动号召 */
          <div
            data-no-pan
            className='relative flex flex-col items-center justify-center rounded-2xl border border-dashed border-border/80 bg-card/60 p-8 text-center shadow-lg backdrop-blur-md sm:max-w-md'
          >
            <div className='mb-4 flex h-16 w-16 items-center justify-center rounded-2xl bg-primary/10 text-primary ring-8 ring-primary/5'>
              <MapIcon className='h-8 w-8' />
            </div>
            <h3 className='text-base font-bold tracking-tight text-foreground'>
              {t('canvas.emptyMapTitle')}
            </h3>
            <p className='mt-2 text-xs leading-relaxed text-muted-foreground'>
              {t('canvas.emptyMapDesc')}
            </p>
            {onUploadMap && (
              <Button
                size='sm'
                className='mt-5 h-9 gap-2 px-5 shadow-sm'
                onClick={onUploadMap}
              >
                <Plus className='h-4 w-4' />
                {t('canvas.uploadMapAction')}
              </Button>
            )}
          </div>
        )}
      </div>

      {currentMap && (
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
          <span className='w-12 px-1 text-center font-mono text-[11px] text-muted-foreground'>
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
            onClick={fitView}
            aria-label={t('canvas.fitView')}
            title={t('canvas.fitView')}
          >
            <Maximize2 className='h-3.5 w-3.5' />
          </Button>
        </div>
      )}
    </div>
  )
}
