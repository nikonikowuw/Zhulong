/**
 * 呈现调度器：按媒体时间（PTS）排期呈现，而不是「有帧就画」。
 *
 * 为什么必须有它：ZLM2 每帧的**到达**时刻由网络与解码抖动决定，而显示刷新是固定的。
 * 若拿到帧就在下一个 vsync 上画，会同时产生两种可见的顿挫：
 *   1. 显示间隔被量化到刷新周期 —— 25fps（帧间隔 40ms）在 60Hz 上本就在 33/50ms 之间
 *      交替，叠加 ±20ms 的到达抖动后彻底无规律；
 *   2. 两帧落进同一个刷新区间时，先到的那帧被静默丢弃 —— 随机丢整帧比任何抖动都显眼。
 * 两者的共同特征是「帧率统计完全正常，但画面一直轻微顿挫」，与算力无关。
 *
 * 这里给出一段人为的目标延迟作为抖动缓冲：帧按 PTS 排期到「应呈现的时刻」，
 * 早到就等、晚到立刻补上，突发时只丢弃真正错过呈现窗口的帧。
 *
 * ⚠️ 延迟与平滑是明确取舍：目标延迟越大越平滑，但越不实时。
 *
 * 本类刻意不依赖 DOM、VideoFrame 或 rAF，只做纯时间算术与队列管理，
 * 以便用表驱动单测覆盖抖动、突发、时间轴跳变、后台挂起等形态。
 */

/** 目标延迟（毫秒）：为网络与解码抖动预留的缓冲深度。100ms ≈ 2.5 帧 @25fps。 */
const DEFAULT_TARGET_LATENCY_MS = 100

/**
 * 允许的「超前量」（毫秒）。
 *
 * 当头帧的排期时刻比 now 超前 target + lead 时，说明源流时间轴跳到了墙钟前面
 * （服务端整段丢帧后等下一个 IDR、相机停流重连等）。此时若不重锚，画面会**静默冻结**
 * 整段时间 —— 这正是「一次抖动放大成 1~4 秒卡死」在显示层的成因。
 */
const DEFAULT_MAX_LEAD_MS = 250

/** 队列上限（帧）：防止 rAF 被挂起（后台标签页）时无限堆积。 */
const DEFAULT_MAX_QUEUE_FRAMES = 24

/**
 * 队列最大时间深度（毫秒）。
 *
 * 这是「追平实时」的硬约束：起播时服务端会补发整个 GOP（可达 4 MiB / 128 帧，
 * 即一两秒内容），若只按**帧数**裁顶，队列会永久保持着满窗陈旧帧 —— 画面虽然
 * 不抖了，但恒定落后实时一整个队列的时间，起播观感就是「卡一下才动」。
 *
 * ⚠️ 这是**突发容忍度**，不是延迟旋钮，绝不能贴着 targetLatencyMs 配：
 * RTSP over TCP 的帧天然成簇到达（一簇 3~10 帧 = 120~400ms），解码器也成簇输出。
 * 曾经配成 250ms，正常成簇就被判为超深，稳态每秒丢好几帧，表现为「画面一直在顿」
 * —— 比它本要修的抖动更糟。500ms（12 帧 @25fps）既覆盖正常成簇，又能在起播时
 * 把一两秒的 GOP 补发裁掉。
 */
const DEFAULT_MAX_DEPTH_MS = 500

/**
 * 判定时间轴整段回退的阈值（微秒）。
 *
 * 小于该阈值的回退按 B 帧重排处理（沿用已排期时刻，绝不回拨时钟）；
 * 超过则视为相机重启 / RTSP 会话重建，丢弃队列并重建时钟。
 */
const DEFAULT_MAX_BACKWARD_JUMP_US = 1_000_000

export interface PresentationSchedulerOptions {
  /** 目标延迟（毫秒）。 */
  targetLatencyMs?: number
  /** 允许的超前量（毫秒）。 */
  maxLeadMs?: number
  /** 队列上限（帧）。 */
  maxQueueFrames?: number
  /** 队列最大时间深度（毫秒）。 */
  maxDepthMs?: number
  /** 判定时间轴整段回退的阈值（微秒）。 */
  maxBackwardJumpUs?: number
}

export interface ScheduledPresentation<T> {
  /** 此刻应当呈现的帧；多个帧同时到期时取最新一帧。null 表示尚未到达呈现时刻。 */
  present: T | null
  /** 错过了呈现窗口、必须由调用方释放的帧。 */
  expired: T[]
  /** 本次是否因源流时间轴跳到墙钟前面而重建了时钟。 */
  rebased: boolean
  /** 本次呈现后仍在等待排期的帧数。 */
  pending: number
}

/**
 * 呈现时钟的只读快照：`PTS → 墙钟` 的当前映射。
 *
 * 音频侧靠它把自己的 PTS 折算到同一条时间轴上（音画同步的唯一依据）。
 * 刻意只读：调度器的时钟由队列自己维护，外部改写会让「队列跑空即作废」的语义失效。
 */
export interface PresentationClockSnapshot {
  /** 建立映射时的墙钟时刻（performance 时间轴，毫秒）。 */
  wallMs: number
  /** 建立映射时的媒体时刻（微秒）。 */
  ptsUS: number
  /** 目标延迟（毫秒）。 */
  targetLatencyMs: number
}

export interface ScheduledPush<T> {
  /** 本次是否因队列上限或时间轴回退被丢弃的帧，必须由调用方释放。 */
  dropped: T[]
  /** 本次是否因时间轴回退而重建了时钟。 */
  rebased: boolean
}

interface QueueEntry<T> {
  ptsUS: number
  payload: T
}

interface PresentationClock {
  /** 建立映射时的墙钟时刻（performance 时间轴，毫秒）。 */
  wallMs: number
  /** 建立映射时的媒体时刻（微秒）。 */
  ptsUS: number
}

export class PresentationScheduler<T> {
  private targetLatencyMs: number
  private readonly maxLeadMs: number
  private readonly maxQueueFrames: number
  private readonly maxDepthMs: number
  private readonly maxBackwardJumpUs: number
  private queue: QueueEntry<T>[] = []
  private clock: PresentationClock | null = null
  private lastQueuedPTSUS: number | null = null

  constructor(options: PresentationSchedulerOptions = {}) {
    this.targetLatencyMs = options.targetLatencyMs ?? DEFAULT_TARGET_LATENCY_MS
    this.maxLeadMs = options.maxLeadMs ?? DEFAULT_MAX_LEAD_MS
    this.maxQueueFrames = options.maxQueueFrames ?? DEFAULT_MAX_QUEUE_FRAMES
    this.maxDepthMs = options.maxDepthMs ?? DEFAULT_MAX_DEPTH_MS
    this.maxBackwardJumpUs =
      options.maxBackwardJumpUs ?? DEFAULT_MAX_BACKWARD_JUMP_US
  }

  /**
   * 入队一帧。
   *
   * @param ptsUS 该帧的显示时间戳（微秒），来自 ZLM2 帧头
   * @param payload 帧载体（实际使用中为 VideoFrame）
   * @param nowMs 当前墙钟（performance.now()）
   */
  push(ptsUS: number, payload: T, nowMs: number): ScheduledPush<T> {
    let dropped: T[] = []
    let rebased = false
    let effectivePTSUS = ptsUS

    if (this.lastQueuedPTSUS !== null && ptsUS < this.lastQueuedPTSUS) {
      if (this.lastQueuedPTSUS - ptsUS > this.maxBackwardJumpUs) {
        // 时间轴整段回退：旧时间轴上的待呈现帧全部作废
        dropped = this.clear()
        rebased = true
      } else {
        // B 帧重排：沿用已排期时刻，保证队列内 PTS 单调、绝不回拨时钟
        effectivePTSUS = this.lastQueuedPTSUS
      }
    }

    if (this.clock === null) {
      this.clock = { wallMs: nowMs, ptsUS: effectivePTSUS }
    }
    this.lastQueuedPTSUS = effectivePTSUS
    this.queue.push({ ptsUS: effectivePTSUS, payload })

    // 先按时间深度裁（保住实时性），再按帧数兜底（保证内存有界）。
    // 两个判据都以「最旧一端」为裁剪对象：丢掉的是已经错过呈现窗口的历史帧，
    // 不是随机丢参考帧。
    while (this.queue.length > 1 && this.depthMs() > this.maxDepthMs) {
      const stale = this.queue.shift()
      if (stale) dropped.push(stale.payload)
    }
    while (this.queue.length > this.maxQueueFrames) {
      const oldest = this.queue.shift()
      if (oldest) dropped.push(oldest.payload)
    }
    return { dropped, rebased }
  }

  /**
   * 取出此刻应当呈现的帧。
   *
   * 同一拍内到期的多帧只呈现最新一帧，其余作为 expired 返回 —— 它们已经错过了
   * 呈现窗口，强行逐帧补画只会把延迟越拖越长。
   */
  takeDue(nowMs: number): ScheduledPresentation<T> {
    if (this.queue.length === 0) {
      // 队列已空（上一拍就画完了）：保持空时钟状态，等下一帧重新建立映射。
      this.clock = null
      this.lastQueuedPTSUS = null
      return { present: null, expired: [], rebased: false, pending: 0 }
    }

    const head = this.queue[0]
    let rebased = false
    if (this.clock === null) {
      this.clock = { wallMs: nowMs, ptsUS: head.ptsUS }
    } else if (
      this.dueOf(head.ptsUS) - nowMs >
      this.targetLatencyMs + this.maxLeadMs
    ) {
      // 源流时间跳到了墙钟前面：把时钟重锚到头帧，避免静默冻结
      this.clock = { wallMs: nowMs, ptsUS: head.ptsUS }
      rebased = true
    }

    const expired: T[] = []
    let present: T | undefined
    while (this.queue.length > 0 && this.dueOf(this.queue[0].ptsUS) <= nowMs) {
      const due = this.queue.shift()
      if (!due) break
      if (present !== undefined) expired.push(present)
      present = due.payload
    }

    if (this.queue.length === 0) {
      // 本次把队列跑空：立即作废时钟。下一帧重新建立映射，
      // 否则「拍平实时」的状态会持续下去，后续帧全部立即到期、零缓冲（抖动重新透传）。
      this.clock = null
      this.lastQueuedPTSUS = null
    }

    return {
      present: present ?? null,
      expired,
      rebased,
      pending: this.queue.length,
    }
  }

  /** 清空队列并返回被丢弃的帧（调用方负责释放）。时钟随之作废。 */
  clear(): T[] {
    const payloads = this.queue.map((entry) => entry.payload)
    this.queue = []
    this.clock = null
    this.lastQueuedPTSUS = null
    return payloads
  }

  /** 当前等待呈现的帧数。 */
  get pending(): number {
    return this.queue.length
  }

  /**
   * 动态调整目标延迟（毫秒），无需清空待呈现队列或重建底层解码器。
   *
   * 典型场景：开启主流 AI 叠加层时适度抬高呈现缓冲（如 160ms）以掩盖推理回传延迟，
   * 关闭时平顺回落至默认低延迟（100ms）；音频侧经 `clockSnapshot()` 自动同步同一偏移。
   */
  setTargetLatencyMs(targetLatencyMs: number): void {
    if (!Number.isFinite(targetLatencyMs) || targetLatencyMs < 0) return
    this.targetLatencyMs = targetLatencyMs
  }

  /**
   * 只读时钟快照。
   *
   * `clock === null` 时返回 null —— 这**不是**错误状态：队列跑空后时钟有意作废，
   * 下一帧进来会重新建立映射。调用方（音频侧）必须把 null 当作「视频尚未建立映射」，
   * 而不是「延迟为 0」。
   */
  clockSnapshot(): PresentationClockSnapshot | null {
    const clock = this.clock
    if (!clock) return null
    return {
      wallMs: clock.wallMs,
      ptsUS: clock.ptsUS,
      targetLatencyMs: this.targetLatencyMs,
    }
  }

  private dueOf(ptsUS: number): number {
    const clock = this.clock
    if (!clock) return Number.NEGATIVE_INFINITY
    return clock.wallMs + (ptsUS - clock.ptsUS) / 1000 + this.targetLatencyMs
  }

  /** 队列首尾帧的排期时刻差（毫秒），即当前的缓冲深度。 */
  private depthMs(): number {
    const head = this.queue[0]
    const newest = this.queue[this.queue.length - 1]
    if (!head || !newest) return 0
    return this.dueOf(newest.ptsUS) - this.dueOf(head.ptsUS)
  }
}
