import {
  type AIResultEvent,
  type AIResultEventInput,
  type RecognitionRecordItem,
  aiResultEventSchema,
  parseAIResultEvent,
} from '../types/ai-result'

type CameraSubscriber = (event: AIResultEvent) => void
type RecordSubscriber = (record: RecognitionRecordItem) => void

/**
 * AI 感知推理结果与实时抓拍流单例管理服务 (AIResultService)
 * 采用浏览器端单例 EventSource 订阅 /api/v1/cameras/events，按 camera_id 派发事件，
 * 并维持实时抓拍流记录环形缓冲区与防抖节流。
 */
export class AIResultService {
  private static instance: AIResultService | null = null

  private eventSource: EventSource | null = null
  private cameraSubscribers = new Map<string, Set<CameraSubscriber>>()
  private recordSubscribers = new Set<RecordSubscriber>()

  private recentRecords: RecognitionRecordItem[] = []
  private readonly maxRecords = 100

  // 抓拍记录防抖冷却表：key = `${cameraId}:${personId || 'unknown'}`，value = 上次入库时间戳
  private recordCooldowns = new Map<string, number>()
  private readonly cooldownMs = 3000

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
   * 订阅全局实时识别抓拍卡片流
   */
  public subscribeRecords(callback: RecordSubscriber): () => void {
    if (this.cleanupTimer) {
      clearTimeout(this.cleanupTimer)
      this.cleanupTimer = null
    }

    this.recordSubscribers.add(callback)
    this.ensureConnected()

    return () => {
      this.recordSubscribers.delete(callback)
      this.checkAutoDisconnect()
    }
  }

  /**
   * 获取最近的历史抓拍记录
   */
  public getRecentRecords(): RecognitionRecordItem[] {
    return [...this.recentRecords]
  }

  /**
   * 清空实时抓拍记录
   */
  public clearRecords(): void {
    this.recentRecords = []
    this.recordCooldowns.clear()
  }

  /**
   * 派发 AI 结果（可用于外部或者测试注入）
   */
  public dispatchEvent(rawEvent: AIResultEvent | AIResultEventInput): void {
    const event = aiResultEventSchema.parse(rawEvent)
    const subs = this.cameraSubscribers.get(event.camera_id)
    if (subs) {
      subs.forEach((cb) => {
        try {
          cb(event)
        } catch {
          // 保护派发安全
        }
      })
    }

    // 处理实时抓拍卡片生成
    this.extractAndEmitRecords(event)
  }

  private extractAndEmitRecords(event: AIResultEvent): void {
    if (!event.persons || event.persons.length === 0) return

    const now = Date.now()
    for (const person of event.persons) {
      for (const face of person.faces) {
        const dedupeKey = `${event.camera_id}:${face.person_id || 'unknown'}`
        const lastTime = this.recordCooldowns.get(dedupeKey) || 0

        // 3秒内同一人/未识别目标不重复刷屏
        if (now - lastTime < this.cooldownMs) {
          continue
        }
        this.recordCooldowns.set(dedupeKey, now)

        const record: RecognitionRecordItem = {
          id: `rec_${now}_${Math.random().toString(36).slice(2, 7)}`,
          cameraId: event.camera_id,
          personId: face.person_id,
          personName: face.person_name,
          matched: face.matched,
          similarity: face.similarity,
          box: face.box,
          timestamp: now,
          pts: event.pts,
        }

        this.recentRecords.unshift(record)
        if (this.recentRecords.length > this.maxRecords) {
          this.recentRecords.pop()
        }

        this.recordSubscribers.forEach((cb) => {
          try {
            cb(record)
          } catch {
            // 保护通知安全
          }
        })
      }
    }
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
    const totalSubscribers =
      this.cameraSubscribers.size + this.recordSubscribers.size
    if (totalSubscribers === 0) return

    const delay = Math.min(1000 * Math.pow(2, this.retryAttempts), 15000)
    this.retryAttempts++

    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null
      this.ensureConnected()
    }, delay)
  }

  private checkAutoDisconnect(): void {
    const totalSubscribers =
      this.cameraSubscribers.size + this.recordSubscribers.size
    if (totalSubscribers === 0 && !this.cleanupTimer) {
      // 5秒优雅防抖：若所有窗口关闭订阅，断开 SSE 节省资源
      this.cleanupTimer = setTimeout(() => {
        this.cleanupTimer = null
        const currentTotal =
          this.cameraSubscribers.size + this.recordSubscribers.size
        if (currentTotal === 0 && this.eventSource) {
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
    this.recordSubscribers.clear()
    this.recentRecords = []
    this.recordCooldowns.clear()
    this.retryAttempts = 0
    this.isConnecting = false
  }
}
