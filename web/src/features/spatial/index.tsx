import { useState } from 'react'
import { Loader2, MapPin, Route, Save, Undo2, Video } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitch } from '@/components/language-switch'
import { Header } from '@/components/layout/header'
import { Main } from '@/components/layout/main'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Search } from '@/components/search'
import { ThemeSwitch } from '@/components/theme-switch'
import { useAllCameras } from '@/features/cameras/contract'
import { type StampingCamera } from './components/canvas/spatial-canvas'
import { SpatialCanvasViewport } from './components/canvas/spatial-canvas-viewport'
import { UnassignedCamerasPanel } from './components/sidebar/unassigned-cameras-panel'
import { MapSwitcher } from './components/toolbar/map-switcher'
import { MapUploadDialog } from './components/toolbar/map-upload-dialog'
import { TrajectorySearchToolbar } from './components/toolbar/trajectory-search-toolbar'
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

export type SpatialMode = 'trajectory' | 'calibration'

export function Spatial() {
  const { t } = useTranslation('spatial')

  const mapsQuery = useSpatialMaps()
  const maps = mapsQuery.data ?? []
  const [selectedMapId, setSelectedMapId] = useState<string | null>(null)

  // 派生当前有效底图：优先用户显式选择，否则取第一张（按创建时间升序）。
  const effectiveMapId = selectedMapId ?? maps[0]?.id ?? null
  const currentMap = maps.find((map) => map.id === effectiveMapId) ?? null

  const camerasQuery = useAllCameras()
  const allCameras = camerasQuery.data?.items ?? []

  const positionsQuery = useCameraPositions(effectiveMapId)
  const serverPositions = positionsQuery.data ?? []

  /** 工作台模式：轨迹分析 vs 点位标定 */
  const [mode, setMode] = useState<SpatialMode>('trajectory')
  const [pendingMode, setPendingMode] = useState<SpatialMode | null>(null)
  const [discardDialogOpen, setDiscardDialogOpen] = useState(false)

  const isCalibrating = mode === 'calibration'

  /** 本地编辑中的点位；null 表示未修改，直接展示服务端数据。 */
  const [customPositions, setCustomPositions] = useState<
    CameraPositionInfo[] | null
  >(null)
  const [stampingCamera, setStampingCamera] = useState<StampingCamera | null>(
    null
  )
  const [uploadDialogOpen, setUploadDialogOpen] = useState(false)

  const displayedPositions = customPositions ?? serverPositions
  const hasUnsavedChanges = customPositions !== null
  const savePositionsMutation = useSaveCameraPositions()

  const trajectoryMutation = useTrajectory()
  const [trajectory, setTrajectory] = useState<TrajectoryResponse | null>(null)

  const handleSelectMapId = (id: string | null) => {
    setSelectedMapId(id)
    setCustomPositions(null)
    setStampingCamera(null)
    setTrajectory(null)
  }

  const handleRequestMode = (nextMode: SpatialMode) => {
    if (mode === nextMode) return
    if (mode === 'calibration' && hasUnsavedChanges) {
      setPendingMode(nextMode)
      setDiscardDialogOpen(true)
      return
    }
    setStampingCamera(null)
    setMode(nextMode)
  }

  const handleConfirmDiscard = () => {
    setCustomPositions(null)
    setStampingCamera(null)
    setDiscardDialogOpen(false)
    if (pendingMode) {
      setMode(pendingMode)
      setPendingMode(null)
    } else {
      setMode('trajectory')
    }
  }

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
  }

  const hasTrajectory = (trajectory?.nodes.length ?? 0) > 0

  return (
    <>
      {/* ===== 统一标准 Header (对齐全站规范) ===== */}
      <Header fixed>
        <Search className='me-auto' />
        <LanguageSwitch />
        <ThemeSwitch />
        <ConfigDrawer />
        <ProfileDropdown />
      </Header>

      {/* ===== 主工作区 ===== */}
      <Main
        fixed
        className='flex flex-1 flex-col gap-2.5 overflow-hidden p-2 sm:p-3 md:p-4'
      >
        {/* 页面标题与主操作区 (对齐 Live / Records / Cameras) */}
        <div className='flex flex-wrap items-center justify-between gap-2 sm:gap-4'>
          <div className='flex flex-wrap items-center gap-3 sm:gap-4'>
            <div>
              <h2 className='text-xl font-bold tracking-tight sm:text-2xl'>
                {t('title')}
              </h2>
              <p className='text-xs text-muted-foreground'>{t('subtitle')}</p>
            </div>

            <MapSwitcher
              selectedMapId={effectiveMapId}
              onSelectMapId={handleSelectMapId}
              onOpenUpload={() => setUploadDialogOpen(true)}
              disabled={isCalibrating && hasUnsavedChanges}
            />
          </div>

          <div className='flex items-center gap-2'>
            {currentMap && (
              <Tabs
                value={mode}
                onValueChange={(val) => handleRequestMode(val as SpatialMode)}
              >
                <TabsList className='h-9'>
                  <TabsTrigger value='trajectory' className='gap-1.5 text-xs'>
                    <Route className='h-3.5 w-3.5' />
                    {t('workbench.modeTrajectory')}
                  </TabsTrigger>
                  <TabsTrigger value='calibration' className='gap-1.5 text-xs'>
                    <MapPin className='h-3.5 w-3.5' />
                    {t('calibration.enter')}
                  </TabsTrigger>
                </TabsList>
              </Tabs>
            )}

            {isCalibrating && (
              <>
                <Button
                  size='sm'
                  className='h-9 gap-1.5'
                  onClick={() => void handleSavePositions()}
                  disabled={
                    !hasUnsavedChanges || savePositionsMutation.isPending
                  }
                >
                  {savePositionsMutation.isPending ? (
                    <Loader2 className='h-3.5 w-3.5 animate-spin' />
                  ) : (
                    <Save className='h-3.5 w-3.5' />
                  )}
                  {t('calibration.save')}
                </Button>

                {hasUnsavedChanges && (
                  <Button
                    variant='outline'
                    size='sm'
                    className='h-9 gap-1 text-muted-foreground'
                    onClick={() => {
                      setCustomPositions(null)
                      setStampingCamera(null)
                    }}
                  >
                    <Undo2 className='h-3.5 w-3.5' />
                    {t('calibration.reset')}
                  </Button>
                )}
              </>
            )}
          </div>
        </div>

        {/* 顶部工具栏 / 状态提示区 */}
        {mode === 'trajectory' && (
          <TrajectorySearchToolbar
            mapId={effectiveMapId}
            isSearching={trajectoryMutation.isPending}
            hasResults={hasTrajectory}
            onSearch={(params) => void handleSearchTrajectory(params)}
            onClear={handleClearTrajectory}
            disabled={!currentMap}
          />
        )}

        {isCalibrating && stampingCamera && (
          <div className='flex items-center justify-between rounded-lg border border-primary/30 bg-primary/10 px-3 py-1.5 text-xs text-primary shadow-2xs'>
            <div className='flex items-center gap-2'>
              <Video className='h-3.5 w-3.5' />
              <span>
                {t('calibration.stampingHint', {
                  name: stampingCamera.cameraName,
                })}
              </span>
            </div>
            <Button
              variant='ghost'
              size='sm'
              className='h-6 px-2 text-xs hover:bg-primary/20 hover:text-primary'
              onClick={() => setStampingCamera(null)}
            >
              {t('common:actions.cancel', '取消')}
            </Button>
          </div>
        )}

        {/* 核心工作区两栏容器 (Master-Detail 现代风格，对齐 Live 监控大盘) */}
        <div className='flex min-h-0 flex-1 gap-2.5 overflow-hidden sm:gap-4'>
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

          <div className='relative flex flex-1 flex-col overflow-hidden rounded-xl border border-border bg-card/40 shadow-xs'>
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

                <SpatialCanvasViewport
                  currentMap={currentMap}
                  positions={displayedPositions}
                  trajectory={trajectory}
                  isCalibrating={isCalibrating}
                  stampingCamera={stampingCamera}
                  onUploadMap={() => setUploadDialogOpen(true)}
                  onClearTrajectory={handleClearTrajectory}
                  onDropCamera={handleDropCamera}
                  onStampCamera={handleStampCamera}
                  onMoveCamera={handleMoveCamera}
                  onAngleChangeCamera={handleAngleChangeCamera}
                  onRemoveCamera={handleRemoveCamera}
                />
              </>
            )}
          </div>
        </div>
      </Main>

      <AlertDialog open={discardDialogOpen} onOpenChange={setDiscardDialogOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t('workbench.exitConfirmTitle')}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t('workbench.exitConfirmDescription')}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel
              onClick={() => {
                setPendingMode(null)
                setDiscardDialogOpen(false)
              }}
            >
              {t('common:actions.cancel', '取消')}
            </AlertDialogCancel>
            <AlertDialogAction onClick={handleConfirmDiscard}>
              {t('calibration.reset')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <MapUploadDialog
        open={uploadDialogOpen}
        onOpenChange={setUploadDialogOpen}
        onSuccess={(newId) => handleSelectMapId(newId)}
      />
    </>
  )
}
