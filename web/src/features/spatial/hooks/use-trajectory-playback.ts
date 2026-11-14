import { useCallback, useEffect, useRef, useState } from 'react'

/** 播放时每个轨迹节点的停留时长（毫秒，1x 倍速）。 */
export const SEGMENT_DURATION_MS = 2000

export interface UseTrajectoryPlaybackOptions {
  totalNodes: number
}

export function useTrajectoryPlayback({
  totalNodes,
}: UseTrajectoryPlaybackOptions) {
  const [activeNodeIndex, setActiveNodeIndex] = useState(0)
  const [isPlaying, setIsPlayingState] = useState(false)
  const [playbackSpeed, setPlaybackSpeed] = useState(1)

  const activeNodeIndexRef = useRef(0)

  const goToNode = useCallback((next: number) => {
    activeNodeIndexRef.current = next
    setActiveNodeIndex(next)
  }, [])

  const setIsPlaying = useCallback(
    (next: boolean) => {
      if (!next) {
        setIsPlayingState(false)
        return
      }
      if (totalNodes < 2) {
        return
      }
      if (activeNodeIndexRef.current >= totalNodes - 1) {
        goToNode(0)
      }
      setIsPlayingState(true)
    },
    [totalNodes, goToNode]
  )

  const resetPlayback = useCallback(() => {
    setIsPlayingState(false)
    setPlaybackSpeed(1)
    goToNode(0)
  }, [goToNode])

  // 轨迹节点步进时钟。光标沿线段的平滑移动由 SVG 动画负责，
  // 这里只负责「当前第几个节点」，避免逐帧 setState 重渲染整张画布。
  useEffect(() => {
    if (!isPlaying) return
    if (totalNodes < 2) return

    const interval = window.setInterval(() => {
      const current = activeNodeIndexRef.current
      const next = current + 1
      if (next >= totalNodes) {
        setIsPlayingState(false)
        return
      }
      goToNode(next)
      if (next >= totalNodes - 1) {
        setIsPlayingState(false)
      }
    }, SEGMENT_DURATION_MS / playbackSpeed)

    return () => window.clearInterval(interval)
  }, [isPlaying, playbackSpeed, totalNodes, goToNode])

  return {
    activeNodeIndex,
    isPlaying,
    playbackSpeed,
    segmentDurationMs: SEGMENT_DURATION_MS / playbackSpeed,
    goToNode,
    resetPlayback,
    setIsPlaying,
    setPlaybackSpeed,
  }
}
