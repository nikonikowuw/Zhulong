import { useEffect, useId, useState, useCallback, useRef } from 'react'
import { StreamConnectionPool } from '../services/stream-pool'
import {
  type ParsedPacket,
  type StreamStats,
  type StreamStatus,
  type StreamType,
} from '../types'

interface UseStreamConnectionOptions {
  cameraId: string | null
  streamType?: StreamType
  enabled?: boolean
  onFrame?: (frame: Uint8Array) => void
  onPacket?: (packet: ParsedPacket) => void
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
  onPacket,
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

  // 保持最新回调引用，避免因外部传入内联闭包导致频繁向连接池重复订阅与断开
  const onFrameRef = useRef(onFrame)
  useEffect(() => {
    onFrameRef.current = onFrame
  }, [onFrame])

  const onPacketRef = useRef(onPacket)
  useEffect(() => {
    onPacketRef.current = onPacket
  }, [onPacket])

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
      onFrame: (frame) => onFrameRef.current?.(frame),
      onPacket: (packet) => onPacketRef.current?.(packet),
    })

    return () => {
      unsubscribe()
    }
  }, [cameraId, streamType, enabled, subscriberId])

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
