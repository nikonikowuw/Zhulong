import {
  type AIResultEvent,
  type AIResultEventInput,
  aiResultEventSchema,
  parseAIResultEvent,
} from '../types/ai-result'

type CameraSubscriber = (event: AIResultEvent) => void

/**
 * AI 感知推理结果单例分发服务 (AIResultService)
 * 采用浏览器端单例 EventSource 订阅 /api/v1/cameras/events，按 camera_id 派发事件。
 * 识别记录的持久化与检索属于 records 特性，本服务不再维护前端环形缓冲区。
 */
export class AIResultService {
  private static instance: AIResultService | null = null

  private eventSource: EventSource | null = null
  private cameraSubscribers = new Map<string, Set<CameraSubscriber>>()

  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private cleanupTimer: ReturnType<typeof setTimeout> | null = null
  private retryAttempts = 0
  private isConnecting = false

  private constructor() {}

  public static getInstance(): AIResultService {
    if (!AIResultService.instance) {
      AIResultService.instance = new AIResultService()
    }
    return AIResultService.instance
  }

  /**
   * 订阅指定摄像机的 AI 推理结果事件
   */
  public subscribeCamera(
    cameraId: string,
    callback: CameraSubscriber
  ): () => void {
    if (this.cleanupTimer) {
      clearTimeout(this.cleanupTimer)
      this.cleanupTimer = null
    }

    let subs = this.cameraSubscribers.get(cameraId)
    if (!subs) {
      subs = new Set()
      this.cameraSubscribers.set(cameraId, subs)
    }
    subs.add(callback)

    this.ensureConnected()

    return () => {
      const currentSubs = this.cameraSubscribers.get(cameraId)
      if (currentSubs) {
        currentSubs.delete(callback)
        if (currentSubs.size === 0) {
          this.cameraSubscribers.delete(cameraId)
        }
      }
      this.checkAutoDisconnect()
    }
  }

  /**
   * 派发 AI 结果（可用于外部或者测试注入）
   */
  public dispatchEvent(rawEvent: AIResultEvent | AIResultEventInput): void {
    const event = aiResultEventSchema.parse(rawEvent)
    const subs = this.cameraSubscribers.get(event.camera_id)
    if (!subs) return

    subs.forEach((cb) => {
      try {
        cb(event)
      } catch {
        // 保护派发安全
      }
    })
  }

  private ensureConnected(): void {
    if (this.eventSource || this.isConnecting) return
    if (typeof EventSource === 'undefined') return

    this.isConnecting = true
    try {
      const es = new EventSource('/api/v1/cameras/events', {
        withCredentials: true,
      })
      this.eventSource = es

      es.onopen = () => {
        this.isConnecting = false
        this.retryAttempts = 0
      }

      es.addEventListener('ai_result', (e: MessageEvent) => {
        const event = parseAIResultEvent(e.data)
        if (event) {
          this.dispatchEvent(event)
        }
      })

      es.onerror = () => {
        this.isConnecting = false
        if (this.eventSource) {
          this.eventSource.close()
          this.eventSource = null
        }
        this.scheduleReconnect()
      }
    } catch {
      this.isConnecting = false
      this.scheduleReconnect()
    }
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer) return
    if (this.cameraSubscribers.size === 0) return

    const delay = Math.min(1000 * Math.pow(2, this.retryAttempts), 15000)
    this.retryAttempts++

    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null
      this.ensureConnected()
    }, delay)
  }

  private checkAutoDisconnect(): void {
    if (this.cameraSubscribers.size === 0 && !this.cleanupTimer) {
      // 5秒优雅防抖：若所有窗口关闭订阅，断开 SSE 节省资源
      this.cleanupTimer = setTimeout(() => {
        this.cleanupTimer = null
        if (this.cameraSubscribers.size === 0 && this.eventSource) {
          this.eventSource.close()
          this.eventSource = null
          if (this.reconnectTimer) {
            clearTimeout(this.reconnectTimer)
            this.reconnectTimer = null
          }
        }
      }, 5000)
    }
  }

  /**
   * 仅用于单元测试重置内部状态
   */
  public resetForTesting(): void {
    if (this.cleanupTimer) clearTimeout(this.cleanupTimer)
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer)
    if (this.eventSource) this.eventSource.close()
    this.eventSource = null
    this.cameraSubscribers.clear()
    this.retryAttempts = 0
    this.isConnecting = false
  }
}
