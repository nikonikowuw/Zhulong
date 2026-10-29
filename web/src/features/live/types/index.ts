/**
 * 实时监控与多画面分屏领域类型定义
 */

export type GridLayout = 1 | 4 | 9 | 16

export type StreamType = 'main' | 'sub'

export type CameraHealthStatus = 'online' | 'offline' | 'error' | 'reconnecting'

export interface LiveCameraItem {
  id: string
  name: string
  /** 相机目标主机（IP 或域名）；后端无法解析时为空，UI 不得伪造 */
  host?: string
  status: CameraHealthStatus
  hasSubStream: boolean
  codec: 'h264' | 'h265'
  /** 主码流分辨率；后端未上报时为空 */
  resolution?: string
}

export interface LiveCellState {
  cellId: number
  cameraId: string | null
  cameraName?: string
  hasSubStream?: boolean
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

/** ZLM2 帧分类，与后端 WireFrame* / Native Zhulong_FRAME_* 一一对应。 */
export type FrameClass = 'unknown' | 'idr' | 'predictive' | 'non_reference'

/** ZLM2 帧头标志位，与后端 WireFlag* 一一对应。 */
export const WIRE_FLAG_KEYFRAME = 0x01
export const WIRE_FLAG_DISCONTINUITY = 0x02
export const WIRE_FLAG_PARAM_INJECTED = 0x04
export const WIRE_FLAG_TIMESTAMP_SYNTHETIC = 0x08
export const WIRE_FLAG_DTS_SYNTHETIC = 0x10

export interface ParsedPacket {
  codec: CodecType
  codecByte: number
  flags: number
  frameClass: FrameClass
  isKeyFrame: boolean
  /** 微秒，服务端归一化后的显示时间戳；允许 B 帧在解码包序中回退。 */
  ptsUs: number
  /** 微秒，服务端归一化后的解码时间戳，按包序单调。 */
  dtsUs: number
  /** 流级帧序号，用于统计真实丢帧（区别于本地主动跳帧）。 */
  seq: number
  /** GOP 序号，变化即代表可以安全重建解码器。 */
  gopId: number
  hasDiscontinuity: boolean
  hasInjectedParamSets: boolean
  hasSyntheticPts: boolean
  hasSyntheticDts: boolean
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

export * from './ai-result'
