import { getCameraStreamWsUrl } from '../api/live-camera-api'
import {
  type ManagedStream,
  type ParsedPacket,
  type StreamStats,
  type StreamStatus,
  type StreamSubscriber,
  type StreamType,
} from '../types'
import { isWebCodecsSupported } from './webcodecs-decoder'
import { parseZlm2Packet } from './wire-parser'

/**
 * 前端单例流连接池管理器 (StreamConnectionPool)
 * 纯客户端维护，用于多窗口复用同一台相机的 WebSocket 流，避免带宽与解码冗余。
 */
export class StreamConnectionPool {
  private static instance: StreamConnectionPool | null = null
  private streams = new Map<string, ManagedStream>()
  private readonly gracePeriodMs = 3000

  /**
   * 重连次数提示阈值。
   *
   * ⚠️ 这不是重试上限 —— 边缘设备无人值守，任何「重试 N 次后放弃」
   * 的设计都等于现场必须派人去点按钮。超过该阈值只改变 UI 文案。
   */
  private readonly reconnectNoticeAfter = 5

  private constructor() {}

  public static getInstance(): StreamConnectionPool {
    if (!StreamConnectionPool.instance) {
      StreamConnectionPool.instance = new StreamConnectionPool()
    }
    return StreamConnectionPool.instance
  }

  /**
   * 订阅指定摄像机的视频流
   * 返回解除订阅的清理函数
   */
  public subscribe(
    cameraId: string,
    streamType: StreamType,
    subscriber: StreamSubscriber
  ): () => void {
    const key = this.getStreamKey(cameraId, streamType)
    let stream = this.streams.get(key)

    if (!stream) {
      stream = this.createManagedStream(cameraId, streamType)
      this.streams.set(key, stream)
    }

    // 若处于清理防抖倒计时，立即取消清理
    if (stream.cleanupTimer) {
      clearTimeout(stream.cleanupTimer)
      stream.cleanupTimer = null
    }

    stream.subscribers.set(subscriber.id, subscriber)
    stream.refCount++

    // 向新订阅者同步当前最新状态与统计
    subscriber.onStatusChange(stream.status, stream.lastError)
    subscriber.onStatsUpdate(stream.stats)

    // 0 -> 1 真正激活连接。
    // 除 connected / connecting 之外的所有状态都要重连：尤其是 'error'
    // 和 'closed'，否则 React 严格模式重挂载或路由往返会让流永远卡在错误态。
    if (
      stream.refCount === 1 &&
      stream.status !== 'connected' &&
      stream.status !== 'connecting'
    ) {
      stream.reconnectAttempts = 0
      this.connect(stream)
    }

    // 返回取消订阅闭包
    return () => {
      this.unsubscribe(key, subscriber.id)
    }
  }

  private unsubscribe(key: string, subscriberId: string): void {
    const stream = this.streams.get(key)
    if (!stream) return

    stream.subscribers.delete(subscriberId)
    stream.refCount = Math.max(0, stream.refCount - 1)

    // N -> 0: 启动 Grace Period 延时断开防抖
    if (stream.refCount === 0) {
      if (stream.cleanupTimer) {
        clearTimeout(stream.cleanupTimer)
      }
      stream.cleanupTimer = setTimeout(() => {
        this.destroyStream(key)
      }, this.gracePeriodMs)
    }
  }

  private createManagedStream(
    cameraId: string,
    streamType: StreamType
  ): ManagedStream {
    return {
      cameraId,
      streamType,
      url: getCameraStreamWsUrl(cameraId, streamType),
      refCount: 0,
      subscribers: new Map(),
      status: 'idle',
      stats: {
        fps: 0,
        resolution: '1920x1080',
        bitrateKbps: 0,
        decoderMode: isWebCodecsSupported() ? 'WebCodecs' : 'WASM',
      },
      statsFrameCount: 0,
      statsByteCount: 0,
      lastStatsTime: Date.now(),
      ws: null,
      reconnectAttempts: 0,
      reconnectTimer: null,
      cleanupTimer: null,
    }
  }

  private connect(stream: ManagedStream): void {
    if (stream.ws || stream.status === 'connected') return

    this.updateStatus(stream, 'connecting')

    try {
      const ws = new WebSocket(stream.url)
      ws.binaryType = 'arraybuffer'
      stream.ws = ws

      ws.onopen = () => {
        stream.reconnectAttempts = 0
        this.updateStatus(stream, 'connected')
      }

      ws.onmessage = (event) => {
        if (event.data instanceof ArrayBuffer) {
          const buffer = event.data
          const now = Date.now()
          const elapsed = (now - stream.lastStatsTime) / 1000

          stream.statsFrameCount++
          stream.statsByteCount += buffer.byteLength

          if (elapsed >= 1.0) {
            const fps = Math.round(stream.statsFrameCount / elapsed)
            const bitrateKbps = Math.round(
              (stream.statsByteCount * 8) / elapsed / 1000
            )
            stream.stats = {
              ...stream.stats,
              fps,
              bitrateKbps,
            }
            stream.statsFrameCount = 0
            stream.statsByteCount = 0
            stream.lastStatsTime = now
            stream.subscribers.forEach((sub) => sub.onStatsUpdate(stream.stats))
          }

          let parsedPacket: ParsedPacket | null = null
          try {
            parsedPacket = parseZlm2Packet(buffer)
          } catch {
            // 忽略非 ZLM2 格式或损坏数据包
          }

          const rawBytes = parsedPacket
            ? parsedPacket.payload
            : new Uint8Array(buffer)

          // 广播帧数据给所有订阅窗口
          stream.subscribers.forEach((sub) => {
            if (parsedPacket) {
              sub.onPacket?.(parsedPacket)
            }
            sub.onFrame?.(rawBytes)
          })
        }
      }

      ws.onerror = () => {
        this.updateStatus(stream, 'error', '网络连接发生异常')
      }

      ws.onclose = () => {
        stream.ws = null
        if (stream.refCount > 0) {
          this.scheduleReconnect(stream)
        } else {
          this.updateStatus(stream, 'closed')
        }
      }
    } catch {
      this.updateStatus(stream, 'error', 'WebSocket 实例创建失败')
      this.scheduleReconnect(stream)
    }
  }

  private scheduleReconnect(stream: ManagedStream): void {
    stream.reconnectAttempts++

    // 指数退避 + 抖动：1s → 2s → 4s → 8s → 16s 封顶。
    // 抖动是必需的：16 路同时断线后若不带抖动重连，等于对设备发起一次小型 DDoS，
    // 而边缘 A55 的 Gin 协程池撑不住这种同步脉冲。
    const base = Math.min(
      1000 * Math.pow(2, stream.reconnectAttempts - 1),
      16000
    )
    const delay = Math.round(base * (0.5 + Math.random() * 0.5))

    // 永不放弃：只在文案上区分「还在正常重试」与「已经重试很多次了」
    const message =
      stream.reconnectAttempts > this.reconnectNoticeAfter
        ? `持续重连中（第 ${stream.reconnectAttempts} 次），请检查设备与网络`
        : `连接已断开，正在重连（第 ${stream.reconnectAttempts} 次）...`

    this.updateStatus(stream, 'reconnecting', message)

    if (stream.reconnectTimer) {
      clearTimeout(stream.reconnectTimer)
    }

    stream.reconnectTimer = setTimeout(() => {
      stream.reconnectTimer = null
      if (stream.refCount > 0) {
        this.connect(stream)
      }
    }, delay)
  }

  public retryNow(cameraId: string, streamType: StreamType): void {
    const key = this.getStreamKey(cameraId, streamType)
    const stream = this.streams.get(key)
    if (!stream) return

    if (stream.reconnectTimer) {
      clearTimeout(stream.reconnectTimer)
      stream.reconnectTimer = null
    }
    if (stream.ws) {
      stream.ws.close()
      stream.ws = null
    }
    stream.reconnectAttempts = 0
    this.connect(stream)
  }

  private updateStatus(
    stream: ManagedStream,
    status: StreamStatus,
    errorMsg?: string
  ): void {
    stream.status = status
    stream.lastError = errorMsg
    stream.subscribers.forEach((sub) => {
      sub.onStatusChange(status, errorMsg)
    })
  }

  public updateStats(
    cameraId: string,
    streamType: StreamType,
    partial: Partial<StreamStats>
  ): void {
    const key = this.getStreamKey(cameraId, streamType)
    const stream = this.streams.get(key)
    if (!stream) return

    stream.stats = { ...stream.stats, ...partial }
    stream.subscribers.forEach((sub) => {
      sub.onStatsUpdate(stream.stats)
    })
  }

  private destroyStream(key: string): void {
    const stream = this.streams.get(key)
    if (!stream) return

    if (stream.cleanupTimer) {
      clearTimeout(stream.cleanupTimer)
      stream.cleanupTimer = null
    }
    if (stream.reconnectTimer) {
      clearTimeout(stream.reconnectTimer)
      stream.reconnectTimer = null
    }
    if (stream.ws) {
      stream.ws.close()
      stream.ws = null
    }
    this.updateStatus(stream, 'closed')
    this.streams.delete(key)
  }

  private getStreamKey(cameraId: string, streamType: StreamType): string {
    return `${cameraId}:${streamType}`
  }

  public getActiveStreamCount(): number {
    return this.streams.size
  }

  public getStreamRefCount(cameraId: string, streamType: StreamType): number {
    const stream = this.streams.get(this.getStreamKey(cameraId, streamType))
    return stream ? stream.refCount : 0
  }
}
