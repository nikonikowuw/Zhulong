import {
  type CameraPositionInfo,
  type CanvasMap,
  type TrajectoryResponse,
} from '../../data/schema'
import { TrajectoryTimelinePlayer } from '../playback/trajectory-timeline-player'
import { SpatialCanvas, type StampingCamera } from './spatial-canvas'

export interface TrajectoryPlaybackState {
  activeNodeIndex: number
  isPlaying: boolean
  playbackSpeed: number
  segmentDurationMs: number
  goToNode: (index: number) => void
  setIsPlaying: (playing: boolean) => void
  setPlaybackSpeed: (speed: number) => void
}

interface SpatialCanvasViewportProps {
  currentMap: CanvasMap | null
  positions: CameraPositionInfo[]
  trajectory: TrajectoryResponse | null
  playback: TrajectoryPlaybackState
  isCalibrating: boolean
  stampingCamera: StampingCamera | null
  onUploadMap?: () => void
  onClearTrajectory: () => void
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

export function SpatialCanvasViewport({
  currentMap,
  positions,
  trajectory,
  playback,
  isCalibrating,
  stampingCamera,
  onUploadMap,
  onClearTrajectory,
  onDropCamera,
  onStampCamera,
  onMoveCamera,
  onAngleChangeCamera,
  onRemoveCamera,
}: SpatialCanvasViewportProps) {
  const trajectoryNodes = trajectory?.nodes ?? []
  const hasTrajectory = trajectoryNodes.length > 0

  return (
    <>
      <SpatialCanvas
        currentMap={currentMap}
        positions={positions}
        trajectoryNodes={trajectoryNodes}
        activeTrajectoryNodeIndex={
          hasTrajectory && !isCalibrating ? playback.activeNodeIndex : undefined
        }
        segmentDurationMs={playback.segmentDurationMs}
        isPlayingTrajectory={playback.isPlaying && !isCalibrating}
        isCalibrating={isCalibrating}
        stampingCamera={stampingCamera}
        onUploadMap={onUploadMap}
        onDropCamera={onDropCamera}
        onStampCamera={onStampCamera}
        onMoveCamera={onMoveCamera}
        onAngleChangeCamera={onAngleChangeCamera}
        onRemoveCamera={onRemoveCamera}
      />

      {hasTrajectory && !isCalibrating && (
        <div className='pointer-events-none absolute inset-x-0 bottom-6 z-30 flex justify-center px-4'>
          <div className='pointer-events-auto w-full max-w-2xl'>
            <TrajectoryTimelinePlayer
              nodes={trajectoryNodes}
              currentNodeIndex={playback.activeNodeIndex}
              isPlaying={playback.isPlaying}
              speed={playback.playbackSpeed}
              onNodeIndexChange={playback.goToNode}
              onPlayingChange={playback.setIsPlaying}
              onSpeedChange={playback.setPlaybackSpeed}
              onClose={onClearTrajectory}
            />
          </div>
        </div>
      )}
    </>
  )
}
