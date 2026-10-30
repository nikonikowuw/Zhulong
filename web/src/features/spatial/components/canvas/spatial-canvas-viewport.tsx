import { useEffect } from 'react'
import {
  type CameraPositionInfo,
  type CanvasMap,
  type TrajectoryResponse,
} from '../../data/schema'
import { useTrajectoryPlayback } from '../../hooks/use-trajectory-playback'
import { TrajectoryTimelinePlayer } from '../playback/trajectory-timeline-player'
import { SpatialCanvas, type StampingCamera } from './spatial-canvas'

interface SpatialCanvasViewportProps {
  currentMap: CanvasMap | null
  positions: CameraPositionInfo[]
  trajectory: TrajectoryResponse | null
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

  const {
    activeNodeIndex,
    isPlaying,
    playbackSpeed,
    segmentDurationMs,
    goToNode,
    resetPlayback,
    setIsPlaying,
    setPlaybackSpeed,
  } = useTrajectoryPlayback({ totalNodes: trajectoryNodes.length })

  // 轨迹结果变更或清空时，自动重置播放时钟到起始点
  useEffect(() => {
    resetPlayback()
  }, [trajectory, resetPlayback])

  return (
    <>
      <SpatialCanvas
        currentMap={currentMap}
        positions={positions}
        trajectoryNodes={trajectoryNodes}
        activeTrajectoryNodeIndex={
          hasTrajectory && !isCalibrating ? activeNodeIndex : undefined
        }
        segmentDurationMs={segmentDurationMs}
        isPlayingTrajectory={isPlaying && !isCalibrating}
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
              currentNodeIndex={activeNodeIndex}
              isPlaying={isPlaying}
              speed={playbackSpeed}
              onNodeIndexChange={goToNode}
              onPlayingChange={setIsPlaying}
              onSpeedChange={setPlaybackSpeed}
              onClose={onClearTrajectory}
            />
          </div>
        </div>
      )}
    </>
  )
}
