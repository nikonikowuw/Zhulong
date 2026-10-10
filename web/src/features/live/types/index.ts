/**
 * 实时监控与多画面分屏领域类型定义
 */

export type GridLayout = 1 | 4 | 9 | 16

export type StreamType = 'main' | 'sub'

export type CameraHealthStatus = 'online' | 'offline' | 'error' | 'reconnecting'

export interface LiveCameraItem {
  id: string
  name: string
  ip: string
  status: CameraHealthStatus
  hasSubStream: boolean
  codec: 'h264' | 'h265'
  resolution?: string
  mainStreamUrl?: string
  subStreamUrl?: string
}

export interface LiveCellState {
  cellId: number
  cameraId: string | null
  cameraName?: string
  streamType: StreamType
  isMuted: boolean
}

export type StreamStatus =
  | 'idle'
  | 'connecting'
  | 'connected'
  | 'reconnecting'
  | 'error'
  | 'closed'

export type CodecType = 'h264' | 'h265' | 'unknown'

export interface ParsedPacket {
  codec: CodecType
  codecByte: number
  flags: number
  isKeyFrame: boolean
  hasPts: boolean
  hasDts: boolean
  pts: bigint
  dts: bigint
  ptsMs: number
  dtsMs: number
  payload: Uint8Array
}

export interface StreamStats {
  fps: number
  resolution: string
  bitrateKbps: number
  decoderMode: 'WebCodecs' | 'MSE' | 'WASM' | 'Mock'
  latencyMs?: number
}

export interface StreamSubscriber {
  id: string
  onStatusChange: (status: StreamStatus, errorMsg?: string) => void
  onStatsUpdate: (stats: StreamStats) => void
  onFrame?: (frame: Uint8Array) => void
  onPacket?: (packet: ParsedPacket) => void
}

export interface ManagedStream {
  cameraId: string
  streamType: StreamType
  url: string
  refCount: number
  subscribers: Map<string, StreamSubscriber>
  status: StreamStatus
  lastError?: string
  stats: StreamStats
  statsFrameCount: number
  statsByteCount: number
  lastStatsTime: number
  ws: WebSocket | null
  reconnectAttempts: number
  reconnectTimer: ReturnType<typeof setTimeout> | null
  cleanupTimer: ReturnType<typeof setTimeout> | null
}
