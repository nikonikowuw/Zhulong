import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { renderHook } from 'vitest-browser-react'
import {
  SEGMENT_DURATION_MS,
  useTrajectoryPlayback,
} from './use-trajectory-playback'

describe('useTrajectoryPlayback', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('initializes with default values', async () => {
    const { result } = await renderHook(() =>
      useTrajectoryPlayback({ totalNodes: 3 })
    )

    expect(result.current.activeNodeIndex).toBe(0)
    expect(result.current.isPlaying).toBe(false)
    expect(result.current.playbackSpeed).toBe(1)
    expect(result.current.segmentDurationMs).toBe(SEGMENT_DURATION_MS)
  })

  it('steps through nodes on timer tick and pauses upon reaching the last node', async () => {
    const { result, act } = await renderHook(() =>
      useTrajectoryPlayback({ totalNodes: 3 })
    )

    await act(() => {
      result.current.setIsPlaying(true)
    })

    expect(result.current.isPlaying).toBe(true)

    // Advance 1 segment duration
    await act(async () => {
      await vi.advanceTimersByTimeAsync(SEGMENT_DURATION_MS)
    })
    expect(result.current.activeNodeIndex).toBe(1)
    expect(result.current.isPlaying).toBe(true)

    // Advance 2nd segment duration: reaches the last node and pauses immediately
    await act(async () => {
      await vi.advanceTimersByTimeAsync(SEGMENT_DURATION_MS)
    })
    expect(result.current.activeNodeIndex).toBe(2)
    expect(result.current.isPlaying).toBe(false)
  })

  it('does not start playing when fewer than 2 nodes exist', async () => {
    const { result, act } = await renderHook(() =>
      useTrajectoryPlayback({ totalNodes: 1 })
    )

    await act(() => {
      result.current.setIsPlaying(true)
    })

    expect(result.current.isPlaying).toBe(false)
    expect(result.current.activeNodeIndex).toBe(0)
  })

  it('restarts from node 0 when play is pressed at the end of trajectory', async () => {
    const { result, act } = await renderHook(() =>
      useTrajectoryPlayback({ totalNodes: 3 })
    )

    await act(() => {
      result.current.goToNode(2)
    })
    expect(result.current.activeNodeIndex).toBe(2)

    await act(() => {
      result.current.setIsPlaying(true)
    })

    expect(result.current.activeNodeIndex).toBe(0)
    expect(result.current.isPlaying).toBe(true)
  })

  it('resets playback to 0 and stops playing', async () => {
    const { result, act } = await renderHook(() =>
      useTrajectoryPlayback({ totalNodes: 4 })
    )

    await act(() => {
      result.current.setIsPlaying(true)
      result.current.goToNode(2)
      result.current.setPlaybackSpeed(2)
    })

    expect(result.current.activeNodeIndex).toBe(2)
    expect(result.current.playbackSpeed).toBe(2)

    await act(() => {
      result.current.resetPlayback()
    })

    expect(result.current.activeNodeIndex).toBe(0)
    expect(result.current.isPlaying).toBe(false)
    expect(result.current.playbackSpeed).toBe(1)
  })

  it('respects playback speed scaling', async () => {
    const { result, act } = await renderHook(() =>
      useTrajectoryPlayback({ totalNodes: 3 })
    )

    await act(() => {
      result.current.setPlaybackSpeed(2)
      result.current.setIsPlaying(true)
    })

    expect(result.current.segmentDurationMs).toBe(SEGMENT_DURATION_MS / 2)

    await act(async () => {
      await vi.advanceTimersByTimeAsync(SEGMENT_DURATION_MS / 2)
    })
    expect(result.current.activeNodeIndex).toBe(1)
  })
})
