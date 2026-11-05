import { useEffect, useId, useState, useCallback, useRef } from 'react'
import { createPoolColdStartTrace } from '../services/cold-start-trace'
import { StreamConnectionPool } from '../services/stream-pool'
import {
  type ParsedPacket,
  type StreamInfo,
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
  onStreamInfo?: (info: StreamInfo) => void
}

export interface UseStreamConnectionReturn {
  status: StreamStatus
  errorMessage: string | undefined
  stats: StreamStats
  /** 建连后的一次性能力帧；未收到时为 null（UI 据此决定喇叭是否置灰）。 */
  streamInfo: StreamInfo | null
  retry: () => void
}

export function useStreamConnection({
  cameraId,
  streamType = 'main',
  enabled = true,
  onFrame,
  onPacket,
  onStreamInfo,
}: UseStreamConnectionOptions): UseStreamConnectionReturn {
  const subscriberId = useId()
  const [status, setStatus] = useState<StreamStatus>('idle')
  const [errorMessage, setErrorMessage] = useState<string | undefined>()
  const [streamInfo, setStreamInfo] = useState<StreamInfo | null>(null)
  const [stats, setStats] = useState<StreamStats>({
    fps: 0,
    resolution: '1920x1080',
    bitrateKbps: 0,
    decoderMode: 'WebCodecs',
  })

  // 保持最新回调引用，避免因外部传入内联闭包导致频繁向连接池重复订阅与断开
  const onFrameRef = useRef(onFrame)
  const onPacketRef = useRef(onPacket)
  const onStreamInfoRef = useRef(onStreamInfo)

  useEffect(() => {
    onFrameRef.current = onFrame
    onPacketRef.current = onPacket
    onStreamInfoRef.current = onStreamInfo
  }, [onFrame, onPacket, onStreamInfo])

  useEffect(() => {
    if (!enabled || !cameraId) {
      return
    }

    const pool = StreamConnectionPool.getInstance()
    // 传输侧起播追踪：回答「服务端到底多快给了什么」，与渲染侧的 cold_start_render 拼成全链路。
    const trace = createPoolColdStartTrace({ cameraId, streamType })
    const unsubscribe = pool.subscribe(cameraId, streamType, {
      id: subscriberId,
      onStatusChange: (newStatus, error) => {
        setStatus(newStatus)
        setErrorMessage(error)
        trace.observeStatus(newStatus)
      },
      onStatsUpdate: (newStats) => {
        setStats(newStats)
      },
      onFrame: (frame) => onFrameRef.current?.(frame),
      onPacket: (packet) => {
        trace.observePacket(packet)
        onPacketRef.current?.(packet)
      },
      onStreamInfo: (info) => {
        setStreamInfo(info)
        onStreamInfoRef.current?.(info)
      },
    })

    return () => {
      unsubscribe()
      trace.dispose()
      // 换机 / 切码流：能力帧属于上一条流，留着会让新格子的喇叭状态错一轮。
      setStreamInfo(null)
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
    streamInfo: isActive ? streamInfo : null,
    retry,
  }
}
