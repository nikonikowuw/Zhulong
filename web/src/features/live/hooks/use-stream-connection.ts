import { useEffect, useId, useState, useCallback } from 'react'
import { StreamConnectionPool } from '../services/stream-pool'
import { type StreamStats, type StreamStatus, type StreamType } from '../types'

interface UseStreamConnectionOptions {
  cameraId: string | null
  streamType?: StreamType
  enabled?: boolean
  onFrame?: (frame: Uint8Array) => void
}

export interface UseStreamConnectionReturn {
  status: StreamStatus
  errorMessage: string | undefined
  stats: StreamStats
  retry: () => void
}

export function useStreamConnection({
  cameraId,
  streamType = 'main',
  enabled = true,
  onFrame,
}: UseStreamConnectionOptions): UseStreamConnectionReturn {
  const subscriberId = useId()
  const [status, setStatus] = useState<StreamStatus>('idle')
  const [errorMessage, setErrorMessage] = useState<string | undefined>()
  const [stats, setStats] = useState<StreamStats>({
    fps: 0,
    resolution: '1920x1080',
    bitrateKbps: 0,
    decoderMode: 'WebCodecs',
  })

  useEffect(() => {
    if (!enabled || !cameraId) {
      return
    }

    const pool = StreamConnectionPool.getInstance()
    const unsubscribe = pool.subscribe(cameraId, streamType, {
      id: subscriberId,
      onStatusChange: (newStatus, error) => {
        setStatus(newStatus)
        setErrorMessage(error)
      },
      onStatsUpdate: (newStats) => {
        setStats(newStats)
      },
      onFrame,
    })

    return () => {
      unsubscribe()
    }
  }, [cameraId, streamType, enabled, subscriberId, onFrame])

  const retry = useCallback(() => {
    if (cameraId) {
      StreamConnectionPool.getInstance().retryNow(cameraId, streamType)
    }
  }, [cameraId, streamType])

  const isActive = enabled && Boolean(cameraId)

  return {
    status: isActive ? status : 'idle',
    errorMessage: isActive ? errorMessage : undefined,
    stats,
    retry,
  }
}
