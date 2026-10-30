import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitch } from '@/components/language-switch'
import { Header } from '@/components/layout/header'
import { Main } from '@/components/layout/main'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { ThemeSwitch } from '@/components/theme-switch'
import { useAllCameras } from '@/features/cameras/contract'
import {
  SpatialCanvas,
  type StampingCamera,
} from './components/canvas/spatial-canvas'
import { TrajectoryTimelinePlayer } from './components/playback/trajectory-timeline-player'
import { UnassignedCamerasPanel } from './components/sidebar/unassigned-cameras-panel'
import { CalibrationToolbar } from './components/toolbar/calibration-toolbar'
import { MapSwitcher } from './components/toolbar/map-switcher'
import { TrajectorySearchForm } from './components/toolbar/trajectory-search-form'
import {
  type CameraPositionInfo,
  type TrajectoryQueryParams,
  type TrajectoryResponse,
} from './data/schema'
import {
  useCameraPositions,
  useSaveCameraPositions,
} from './hooks/use-camera-positions'
import { useSpatialMaps } from './hooks/use-spatial-maps'
import { useTrajectory } from './hooks/use-trajectory'

/** 播放时每个轨迹节点的停留时长（毫秒，1x 倍速）。 */
const SEGMENT_DURATION_MS = 2000

export function Spatial() {
  const { t } = useTranslation('spatial')

  const mapsQuery = useSpatialMaps()
  const maps = useMemo(() => mapsQuery.data ?? [], [mapsQuery.data])
  const [selectedMapId, setSelectedMapId] = useState<string | null>(null)

  // 派生当前有效底图：优先用户显式选择，否则取第一张（按创建时间升序）。
  const effectiveMapId = selectedMapId ?? maps[0]?.id ?? null
  const currentMap = maps.find((map) => map.id === effectiveMapId) ?? null

  const camerasQuery = useAllCameras()
  const allCameras = useMemo(
    () => camerasQuery.data?.items ?? [],
    [camerasQuery.data]
  )

  const positionsQuery = useCameraPositions(effectiveMapId)
  const serverPositions = useMemo(
    () => positionsQuery.data ?? [],
    [positionsQuery.data]
  )

  /** 本地编辑中的点位；null 表示未修改，直接展示服务端数据。 */
  const [customPositions, setCustomPositions] = useState<
    CameraPositionInfo[] | null
  >(null)
  const [isCalibrating, setIsCalibrating] = useState(false)
  const [stampingCamera, setStampingCamera] = useState<StampingCamera | null>(
    null
  )

  const displayedPositions = customPositions ?? serverPositions
  const hasUnsavedChanges = customPositions !== null
  const savePositionsMutation = useSaveCameraPositions()

  const trajectoryMutation = useTrajectory()
  const [trajectory, setTrajectory] = useState<TrajectoryResponse | null>(null)
  const [activeNodeIndex, setActiveNodeIndex] = useState(0)
  const [isPlaying, setIsPlaying] = useState(false)
  const [playbackSpeed, setPlaybackSpeed] = useState(1)

  const activeNodeIndexRef = useRef(0)

  const goToNode = useCallback((next: number) => {
    activeNodeIndexRef.current = next
    setActiveNodeIndex(next)
  }, [])

  const resetPlayback = useCallback(() => {
    setIsPlaying(false)
    setPlaybackSpeed(1)
    goToNode(0)
  }, [goToNode])

  const handleSelectMapId = (id: string | null) => {
    setSelectedMapId(id)
    setCustomPositions(null)
    setStampingCamera(null)
    setTrajectory(null)
    resetPlayback()
  }

  // 轨迹节点步进时钟。光标沿线段的平滑移动由 SVG 动画负责，
  // 这里只负责「当前第几个节点」，避免逐帧 setState 重渲染整张画布。
  useEffect(() => {
    if (!isPlaying) return
    const total = trajectory?.nodes.length ?? 0
    if (total < 2) return

    const interval = window.setInterval(() => {
      const current = activeNodeIndexRef.current
      if (current >= total - 1) {
        setIsPlaying(false)
        return
      }
      goToNode(current + 1)
    }, SEGMENT_DURATION_MS / playbackSpeed)

    return () => window.clearInterval(interval)
  }, [isPlaying, playbackSpeed, trajectory, goToNode])

  const applyPositions = (
    updater: (base: CameraPositionInfo[]) => CameraPositionInfo[]
  ) => {
    setCustomPositions((prev) => updater(prev ?? serverPositions))
  }

  const handleDropCamera = (
    cameraId: string,
    cameraName: string,
    x: number,
    y: number
  ) => {
    applyPositions((base) => [
      ...base.filter((pos) => pos.cameraId !== cameraId),
      { cameraId, cameraName, x, y, angle: 90 },
    ])
    setStampingCamera(null)
  }

  const handleStampCamera = (x: number, y: number) => {
    if (!stampingCamera) return
    handleDropCamera(stampingCamera.cameraId, stampingCamera.cameraName, x, y)
  }

  const handleMoveCamera = (cameraId: string, x: number, y: number) => {
    applyPositions((base) =>
      base.map((pos) => (pos.cameraId === cameraId ? { ...pos, x, y } : pos))
    )
  }

  const handleAngleChangeCamera = (cameraId: string, angle: number) => {
    applyPositions((base) =>
      base.map((pos) => (pos.cameraId === cameraId ? { ...pos, angle } : pos))
    )
  }

  const handleRemoveCamera = (cameraId: string) => {
    applyPositions((base) => base.filter((pos) => pos.cameraId !== cameraId))
  }

  const handleSavePositions = async () => {
    if (!effectiveMapId || !customPositions) return
    try {
      await savePositionsMutation.mutateAsync({
        mapId: effectiveMapId,
        positions: customPositions.map((pos) => ({
          cameraId: pos.cameraId,
          x: pos.x,
          y: pos.y,
          angle: pos.angle,
        })),
      })
      setCustomPositions(null)
      toast.success(t('calibration.saveSuccess'))
    } catch (err: unknown) {
      toast.error(
        err instanceof Error && err.message
          ? err.message
          : t('calibration.saveFailed')
      )
    }
  }

  const handleSearchTrajectory = async (params: TrajectoryQueryParams) => {
    try {
      const response = await trajectoryMutation.mutateAsync(params)
      setTrajectory(response)
      resetPlayback()
      if (response.nodes.length === 0) {
        toast.info(t('trajectory.noResults'))
      } else {
        toast.success(
          t('trajectory.foundNodes', { count: response.nodes.length })
        )
      }
    } catch (err: unknown) {
      toast.error(
        err instanceof Error && err.message
          ? err.message
          : t('trajectory.searchFailed')
      )
    }
  }

  const handleClearTrajectory = () => {
    setTrajectory(null)
    resetPlayback()
  }

  const trajectoryNodes = trajectory?.nodes ?? []
  const hasTrajectory = trajectoryNodes.length > 0

  return (
    <>
      <Header fixed>
        <div className='flex items-center gap-3'>
          <h1 className='text-lg font-bold tracking-tight'>{t('title')}</h1>
          <MapSwitcher
            selectedMapId={effectiveMapId}
            onSelectMapId={handleSelectMapId}
            disabled={isCalibrating && hasUnsavedChanges}
          />
        </div>

        <div className='ms-auto flex items-center gap-2'>
          <TrajectorySearchForm
            mapId={effectiveMapId}
            isSearching={trajectoryMutation.isPending}
            hasResults={hasTrajectory}
            onSearch={(params) => void handleSearchTrajectory(params)}
            onClear={handleClearTrajectory}
            disabled={isCalibrating}
          />

          {currentMap && (
            <CalibrationToolbar
              isCalibrating={isCalibrating}
              hasUnsavedChanges={hasUnsavedChanges}
              isSaving={savePositionsMutation.isPending}
              onToggleCalibrating={() => {
                if (isCalibrating && hasUnsavedChanges) {
                  setCustomPositions(null)
                }
                setStampingCamera(null)
                setIsCalibrating((current) => !current)
              }}
              onSave={() => void handleSavePositions()}
              onReset={() => setCustomPositions(null)}
            />
          )}

          <div className='mx-1 my-auto h-4 w-px bg-border' />
          <ThemeSwitch />
          <LanguageSwitch />
          <ConfigDrawer />
          <ProfileDropdown />
        </div>
      </Header>

      <Main fixed className='flex h-[calc(100vh-4rem)] overflow-hidden p-0'>
        {isCalibrating && (
          <UnassignedCamerasPanel
            cameras={allCameras}
            positions={displayedPositions}
            selectedCameraId={stampingCamera?.cameraId ?? null}
            onSelectCamera={(camera) =>
              setStampingCamera({
                cameraId: camera.id,
                cameraName: camera.name,
              })
            }
            onRemovePosition={handleRemoveCamera}
          />
        )}

        <div className='relative h-full flex-1 overflow-hidden'>
          {mapsQuery.isLoading ? (
            <div className='flex h-full items-center justify-center p-12'>
              <Skeleton className='h-2/3 w-2/3' />
              <span className='sr-only'>{t('canvas.loading')}</span>
            </div>
          ) : mapsQuery.isError ? (
            <div className='flex h-full flex-col items-center justify-center gap-3 p-12 text-center'>
              <p className='text-sm text-destructive'>
                {t('canvas.loadFailed')}
              </p>
              <Button
                variant='outline'
                size='sm'
                onClick={() => void mapsQuery.refetch()}
              >
                {t('canvas.retry')}
              </Button>
            </div>
          ) : (
            <>
              {positionsQuery.isError && (
                <div className='absolute inset-x-0 top-0 z-30 flex items-center justify-center gap-2 border-b border-border bg-background/95 p-2 text-xs text-destructive'>
                  <span>{t('canvas.loadFailed')}</span>
                  <Button
                    variant='ghost'
                    size='sm'
                    className='h-6 px-2 text-xs'
                    onClick={() => void positionsQuery.refetch()}
                  >
                    {t('canvas.retry')}
                  </Button>
                </div>
              )}
              {positionsQuery.isLoading && (
                <div className='absolute end-4 top-4 z-30 flex items-center gap-1 rounded border border-border bg-background/90 px-2 py-1 text-[11px] text-muted-foreground'>
                  <Loader2 className='h-3 w-3 animate-spin' />
                  {t('canvas.loading')}
                </div>
              )}

              <SpatialCanvas
                currentMap={currentMap}
                positions={displayedPositions}
                trajectoryNodes={trajectoryNodes}
                activeTrajectoryNodeIndex={
                  hasTrajectory ? activeNodeIndex : undefined
                }
                segmentDurationMs={SEGMENT_DURATION_MS / playbackSpeed}
                isPlayingTrajectory={isPlaying}
                isCalibrating={isCalibrating}
                stampingCamera={stampingCamera}
                onDropCamera={handleDropCamera}
                onStampCamera={handleStampCamera}
                onMoveCamera={handleMoveCamera}
                onAngleChangeCamera={handleAngleChangeCamera}
                onRemoveCamera={handleRemoveCamera}
              />

              {stampingCamera && (
                <div className='absolute start-1/2 top-4 z-30 -translate-x-1/2 rounded-full border border-border bg-background/95 px-3 py-1 text-xs text-foreground shadow-sm'>
                  {t('calibration.stampingHint', {
                    name: stampingCamera.cameraName,
                  })}
                </div>
              )}

              {hasTrajectory && (
                <TrajectoryTimelinePlayer
                  nodes={trajectoryNodes}
                  currentNodeIndex={activeNodeIndex}
                  isPlaying={isPlaying}
                  speed={playbackSpeed}
                  onNodeIndexChange={goToNode}
                  onPlayingChange={setIsPlaying}
                  onSpeedChange={setPlaybackSpeed}
                  onClose={handleClearTrajectory}
                />
              )}
            </>
          )}
        </div>
      </Main>
    </>
  )
}
