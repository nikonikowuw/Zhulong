import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { getRouteApi } from '@tanstack/react-router'
import { Loader2, Video } from 'lucide-react'
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
import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitch } from '@/components/language-switch'
import { Header } from '@/components/layout/header'
import { Main } from '@/components/layout/main'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Search } from '@/components/search'
import { ThemeSwitch } from '@/components/theme-switch'
import { useAllCameras } from '@/features/cameras/contract'
import {
  SpatialCanvasViewport,
  type TrajectoryPlaybackState,
} from './components/canvas/spatial-canvas-viewport'
import { UnassignedCamerasPanel } from './components/sidebar/unassigned-cameras-panel'
import { MapUploadDialog } from './components/toolbar/map-upload-dialog'
import { SpatialWorkbenchHeader } from './components/toolbar/spatial-workbench-header'
import { TrajectorySearchToolbar } from './components/toolbar/trajectory-search-toolbar'
import {
  type TrajectoryQueryParams,
  type TrajectoryResponse,
} from './data/schema'
import { useCalibrationDraft } from './hooks/use-calibration-draft'
import { useCameraPositions } from './hooks/use-camera-positions'
import { useSpatialMaps } from './hooks/use-spatial-maps'
import { useTrajectory } from './hooks/use-trajectory'
import { useTrajectoryPlayback } from './hooks/use-trajectory-playback'
import { resolveTimeRange } from './utils/format'

const route = getRouteApi('/_authenticated/spatial/')

export function Spatial() {
  const { t } = useTranslation('spatial')
  const search = route.useSearch()

  const mapsQuery = useSpatialMaps()
  const maps = mapsQuery.data ?? []
  const [selectedMapId, setSelectedMapId] = useState<string | null>(null)

  const deepLinkMapId =
    search.mapId && maps.some((map) => map.id === search.mapId)
      ? search.mapId
      : null

  // 派生当前有效底图：用户显式选择 > 深链接指定底图 > 第一张底图。
  const effectiveMapId = selectedMapId ?? deepLinkMapId ?? maps[0]?.id ?? null
  const currentMap = maps.find((map) => map.id === effectiveMapId) ?? null

  const camerasQuery = useAllCameras()
  const allCameras = camerasQuery.data?.items ?? []

  const positionsQuery = useCameraPositions(effectiveMapId)
  const serverPositions = positionsQuery.data ?? []

  const [uploadDialogOpen, setUploadDialogOpen] = useState(false)

  const draft = useCalibrationDraft({
    mapId: effectiveMapId,
    serverPositions,
  })

  const { mutateAsync: queryTrajectory, isPending: isSearchingTrajectory } =
    useTrajectory()
  const [trajectory, setTrajectory] = useState<TrajectoryResponse | null>(null)
  const totalTrajectoryNodes = trajectory?.nodes.length ?? 0

  const {
    activeNodeIndex,
    isPlaying,
    playbackSpeed,
    segmentDurationMs,
    goToNode,
    resetPlayback,
    setIsPlaying,
    setPlaybackSpeed,
  } = useTrajectoryPlayback({ totalNodes: totalTrajectoryNodes })

  const playbackState: TrajectoryPlaybackState = useMemo(
    () => ({
      activeNodeIndex,
      isPlaying,
      playbackSpeed,
      segmentDurationMs,
      goToNode,
      setIsPlaying,
      setPlaybackSpeed,
    }),
    [
      activeNodeIndex,
      isPlaying,
      playbackSpeed,
      segmentDurationMs,
      goToNode,
      setIsPlaying,
      setPlaybackSpeed,
    ]
  )

  const handleSelectMapId = useCallback(
    (id: string | null) => {
      setSelectedMapId(id)
      draft.resetDraft()
      setTrajectory(null)
      resetPlayback()
    },
    [draft, resetPlayback]
  )

  const handleSearchTrajectory = useCallback(
    async (params: TrajectoryQueryParams) => {
      try {
        const response = await queryTrajectory(params)
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
    },
    [t, queryTrajectory, resetPlayback]
  )

  const lastAutoSearchKeyRef = useRef<string | null>(null)

  // 深链接自动查询：URL 携带目标人员且底图就绪时自动检索一次，换目标或底图时允许重查。
  useEffect(() => {
    if (search.targetType !== 'person' || !search.personId || !effectiveMapId) {
      return
    }
    const searchKey = `${search.targetType}:${search.personId}:${effectiveMapId}`
    if (lastAutoSearchKeyRef.current === searchKey) return
    lastAutoSearchKeyRef.current = searchKey

    const { startTime, endTime } = resolveTimeRange('today')
    void handleSearchTrajectory({
      mapId: effectiveMapId,
      targetType: 'person',
      personId: search.personId,
      startTime,
      endTime,
    })
  }, [
    search.targetType,
    search.personId,
    effectiveMapId,
    handleSearchTrajectory,
  ])

  const handleClearTrajectory = useCallback(() => {
    setTrajectory(null)
    resetPlayback()
  }, [resetPlayback])

  const hasTrajectory = totalTrajectoryNodes > 0

  return (
    <>
      <Header fixed>
        <Search className='me-auto' />
        <LanguageSwitch />
        <ThemeSwitch />
        <ConfigDrawer />
        <ProfileDropdown />
      </Header>

      <Main
        fixed
        className='flex flex-1 flex-col gap-2.5 overflow-hidden p-2 sm:p-3 md:p-4'
      >
        <SpatialWorkbenchHeader
          hasCurrentMap={Boolean(currentMap)}
          selectedMapId={effectiveMapId}
          mode={draft.mode}
          isCalibrating={draft.isCalibrating}
          hasUnsavedChanges={draft.hasUnsavedChanges}
          isSavingPositions={draft.isSavingPositions}
          onSelectMapId={handleSelectMapId}
          onOpenUpload={() => setUploadDialogOpen(true)}
          onRequestMode={draft.handleRequestMode}
          onSavePositions={() => void draft.handleSavePositions()}
          onResetDraft={draft.resetDraft}
        />

        {draft.mode === 'trajectory' && (
          <TrajectorySearchToolbar
            mapId={effectiveMapId}
            isSearching={isSearchingTrajectory}
            hasResults={hasTrajectory}
            onSearch={(params) => void handleSearchTrajectory(params)}
            onClear={handleClearTrajectory}
            disabled={!currentMap}
            initialTargetType={search.targetType}
            initialPersonId={search.personId}
          />
        )}

        {draft.isCalibrating && draft.stampingCamera && (
          <div className='flex items-center justify-between rounded-lg border border-primary/30 bg-primary/10 px-3 py-1.5 text-xs text-primary shadow-2xs'>
            <div className='flex items-center gap-2'>
              <Video className='h-3.5 w-3.5' />
              <span>
                {t('calibration.stampingHint', {
                  name: draft.stampingCamera.cameraName,
                })}
              </span>
            </div>
            <Button
              variant='ghost'
              size='sm'
              className='h-6 px-2 text-xs hover:bg-primary/20 hover:text-primary'
              onClick={() => draft.setStampingCamera(null)}
            >
              {t('common:actions.cancel')}
            </Button>
          </div>
        )}

        <div className='flex min-h-0 flex-1 gap-2.5 overflow-hidden sm:gap-4'>
          {draft.isCalibrating && (
            <UnassignedCamerasPanel
              cameras={allCameras}
              positions={draft.displayedPositions}
              selectedCameraId={draft.stampingCamera?.cameraId ?? null}
              onSelectCamera={(camera) =>
                draft.setStampingCamera({
                  cameraId: camera.id,
                  cameraName: camera.name,
                })
              }
              onCancelStamp={() => draft.setStampingCamera(null)}
              onRemovePosition={draft.handleRemoveCamera}
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
                  positions={draft.displayedPositions}
                  trajectory={trajectory}
                  playback={playbackState}
                  isCalibrating={draft.isCalibrating}
                  stampingCamera={draft.stampingCamera}
                  onUploadMap={() => setUploadDialogOpen(true)}
                  onClearTrajectory={handleClearTrajectory}
                  onDropCamera={draft.handleDropCamera}
                  onStampCamera={draft.handleStampCamera}
                  onMoveCamera={draft.handleMoveCamera}
                  onAngleChangeCamera={draft.handleAngleChangeCamera}
                  onRemoveCamera={draft.handleRemoveCamera}
                />
              </>
            )}
          </div>
        </div>
      </Main>

      <AlertDialog
        open={draft.discardDialogOpen}
        onOpenChange={draft.handleDiscardDialogOpenChange}
      >
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
            <AlertDialogCancel onClick={draft.handleCancelDiscard}>
              {t('common:actions.cancel')}
            </AlertDialogCancel>
            <AlertDialogAction onClick={draft.handleConfirmDiscard}>
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
