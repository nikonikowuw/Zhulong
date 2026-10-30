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

  // 轨迹节点步进时钟。光标沿线段的平滑移动由 SVG 动画负责，
  // 这里只负责「当前第几个节点」，避免逐帧 setState 重渲染整张画布。
  useEffect(() => {
    if (!isPlaying) return
    if (totalNodes < 2) return

    const interval = window.setInterval(() => {
      const current = activeNodeIndexRef.current
      if (current >= totalNodes - 1) {
        setIsPlaying(false)
        return
      }
      goToNode(current + 1)
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
