/**
 * 实时音频播放器：把「已解码的 PCM 帧」按**视频呈现时钟**投进 AudioWorklet 环形缓冲。
 *
 * ## 为什么音频必须挂在视频的时钟上
 *
 * 音画同步只有一个可靠基准：谁也不可能比画面更权威。视频侧的 `PresentationScheduler`
 * 已经建立了 `PTS → 墙钟` 的映射（含 100 ms 抖动缓冲），音频只要复用同一条映射，
 * 两者天然对齐。若音频另建一条时钟，任何一次重锚都会变成可闻的音画错位，
 * 而且错多少完全取决于两条时钟各自的抖动量 —— 无法用偏移常数修正。
 *
 * ## 为什么不在主线程重采样 / 不做 AudioBufferSourceNode 排期
 *
 * 见 `live-audio-worklet.js` 头部说明。主线程只在**投递**时做算术（约 50 次/秒），
 * 播放位置的推进完全在音频线程里。
 *
 * ## 必须知道的浏览器行为
 *
 * 无用户手势时 `ctx.resume()` 的 Promise **既不 resolve 也不 reject**，`await` 它会
 * 永久挂起（实测，见 `research/worklet-loading.md` §2.1）。所以 `start()` 非阻塞地
 * 触发 `void context.resume().catch(...)`，绝不 `await resume()`：`AudioContext` 在构造时处于 `suspended`，
 * `addModule()` 在这个状态下仍能成功；触发 resume() 请求浏览器在具备手势激活时唤醒渲染线程。
 */
import { AUDIO_FLAG_DISCONTINUITY, type AudioFrame } from './audio-channel'
import workletSource from './live-audio-worklet.js?raw'
import { type PresentationClockSnapshot } from './presentation-scheduler'

/**
 * 呈现时钟快照。
 *
 * 形状由 `presentation-scheduler.ts` 单点定义（`clockSnapshot()` 的返回类型），
 * 这里只做转发 —— 两处各写一份 interface 只会在某次改动后悄悄漂移，
 * 而结构类型不会在编译期提醒你。
 */
export type { PresentationClockSnapshot }

/**
 * worklet 的注册名。必须与 `live-audio-worklet.js` 里的 `PROCESSOR_NAME` 逐字一致。
 * 改一处不改另一处，`new AudioWorkletNode()` 会以 `InvalidStateError` 失败。
 */
export const AUDIO_WORKLET_PROCESSOR_NAME = 'zhulong-live-audio'

/**
 * 预滚（毫秒）：重锚后从 `now + preRoll` 起播，而不是「立刻」。
 *
 * 留这一小段是因为重锚通常伴随一次投递突发：立刻起播意味着后续几包必然落后于
 * 播放位置，环深在几百毫秒内被吃光，接着就是欠载 → 再重锚的来回抖动。
 * 120 ms ≈ 6 包 G.711，够吸收一次正常的投递抖动，又远小于人耳对「点了没反应」的容忍。
 */
const PRE_ROLL_MS = 120

/** 播放位置落后于「应当听到的时刻」多少毫秒算陈旧（design.md §4.2：250 ms）。 */
const STALE_THRESHOLD_MS = 250

/** 播放位置超前于「应当听到的时刻」多少毫秒算时间轴跳变（design.md §4.2：400 ms）。 */
const FUTURE_THRESHOLD_MS = 400

/**
 * 环深上限（毫秒）。超过就整段重锚，而不是逐帧丢旧。
 *
 * design.md §4.2 写的是「环深 > 250 ms 丢旧帧」。这里改为**整段 flush + 重锚**：
 *   1. 逐帧丢旧需要知道「当前播放到了哪一帧」，而播放位置在音频线程里，主线程只有
 *      估算值 —— 一旦估偏，丢掉的就是还没播的样本，直接变成断音；
 *   2. 环深超限通常意味着时间轴整体错位（切码流、解码突发、页面挂起后恢复），
 *      逐帧丢旧只能把偏差缩小到「丢完为止」，重锚一步到位；
 *   3. 代码量与出错面都小一个数量级：与 `due > now + 400` 走同一条重锚路径。
 * 代价是一次可闻的短暂静音（约 {@link PRE_ROLL_MS}），换的是不长期偏离画面。
 */
const RING_HIGH_WATER_MS = 250

/** `clock === null` 时最多缓存多久（毫秒）。超时丢弃，绝不无限缓存。 */
const UNMAPPED_HOLD_MS = 100

/** 环形缓冲采样率未知时的兜底输出采样率（绝大多数浏览器是 48000）。 */
const FALLBACK_SAMPLE_RATE = 48_000

/**
 * 一帧音频「真正入环」时会话快照。
 *
 * 存在的理由只有一个：音画同步是**跨模块**的观测量（音频入环时刻 vs 视频绘制时刻），
 * 任何一侧都拿不到全貌。播放器不自己算偏差，只把事实报出来，由测量方（浏览器里的
 * 同步实测用例、现场诊断脚本）把两边拼起来。
 */
export interface AudioScheduleEvent {
  /** 该帧在视频呈现时间轴上的 PTS（微秒）。 */
  ptsUs: number
  /** 该帧**应当开始出声**的墙钟时刻（毫秒，`performance.now()` 域）。 */
  startDueMs: number
  /** 本次入环时的墙钟时刻（毫秒）。 */
  nowMs: number
  /** 该帧是否为一条新时间轴的首帧（含 {@link PRE_ROLL_MS} 预滚）。 */
  reanchored: boolean
}

export interface LiveAudioPlayerOptions {
  /** 拉取当前视频呈现时钟；返回 null 表示视频尚未建立映射。 */
  getClock: () => PresentationClockSnapshot | null
  /**
   * 观测钩子：每帧真正入环时报告一次（默认无操作）。
   *
   * 只在**入环**时报告，不报告被丢弃/暂存的帧 —— 那些帧不会出声，报出来只会让
   * 「应该有声音的时刻」里混进一堆不存在的样本。
   */
  onScheduled?: (event: AudioScheduleEvent) => void
  /** 默认音量 0..1（不持久化）。 */
  volume?: number
  /**
   * 可注入的墙钟（毫秒）。默认 `performance.now()`。
   * 单测用它代替 fake timers：投递策略全是纯算术，注入 `now` 比操纵全局定时器确定得多。
   */
  now?: () => number
}

export interface LiveAudioPlayerStats {
  /** 落后于当前播放位置太多而被丢弃的帧数。 */
  droppedStale: number
  /** 超前太多（时间轴跳变）而被重锚的帧数。 */
  droppedAhead: number
  /**
   * 时间轴**重建**次数：DISCONTINUITY / 时间轴跳变 / 环深超限 / 外部 flush。
   * 首次锚定（`start()` 后的第一包）不计入 —— 那不是重建，是原本就没有。
   */
  rebased: number
  /** 真正投递给 worklet 的帧数。 */
  played: number
  /** 因 `clock === null` 或 worklet 未就绪而暂存的帧数（当前值，非累计）。 */
  held: number
  /** 是否降级为「无声」（AudioWorklet 不可用 / addModule 失败）。 */
  degraded: boolean
}

export interface LiveAudioPlayer {
  /** 解禁时调用：建 AudioContext + 装配 worklet。不 await resume()。 */
  start(): Promise<void>
  /** 投递一帧（G.711 解码后的 PCM 或 AAC 解码后的 PCM，都已由调用方处理 codec）。 */
  push(frame: AudioFrame, pcm: Float32Array, inputRate: number): void
  /** 丢弃全部缓冲并清空重锚（视频 onResync / discontinuity）。 */
  flush(): void
  setMuted(muted: boolean): void
  setVolume(volume: number): void
  close(): Promise<void>
  readonly stats: LiveAudioPlayerStats
}

/** 暂存的一帧（worklet 未就绪 / 视频时钟未建立）。 */
interface HeldFrame {
  frame: AudioFrame
  pcm: Float32Array
  inputRate: number
  /** 进入暂存的墙钟时刻（毫秒），用于 100 ms 超时判定。 */
  heldAtMs: number
}

function clampVolume(volume: number): number {
  if (!Number.isFinite(volume)) return 0
  return Math.min(1, Math.max(0, volume))
}

/**
 * 一帧音频「应当被听到」的墙钟时刻（毫秒）。
 *
 * 与视频侧 `PresentationScheduler.dueOf()` 逐字同式：同一条时间轴，没有额外偏移。
 * 抽成导出函数是为了让单测能直接对表断言，不必穿过 Player 的生命周期。
 */
export function audioDueMs(
  clock: PresentationClockSnapshot,
  ptsUs: number
): number {
  return clock.wallMs + (ptsUs - clock.ptsUS) / 1000 + clock.targetLatencyMs
}

/**
 * 建立播放器。
 *
 * 生命周期与 `AudioContext` 严格绑定在 `start()` / `close()` 之间：不 start 就永远
 * 不会创建上下文（遵守 Autoplay Policy：AudioContext 只在首次手势时诞生）。
 */
export function createLiveAudioPlayer(
  options: LiveAudioPlayerOptions
): LiveAudioPlayer {
  const { getClock } = options
  const now = options.now ?? (() => performance.now())

  let ctx: AudioContext | null = null
  let node: AudioWorkletNode | null = null
  let gain: GainNode | null = null
  let outputRate = FALLBACK_SAMPLE_RATE

  /** `addModule()` 的进行中 Promise。首帧投递等它就绪（落到 `held`）。 */
  let ready: Promise<void> | null = null
  /** `ready` 已 resolve 且图已建好。 */
  let attached = false
  let closed = false

  let muted = false
  let volume = clampVolume(options.volume ?? 1)

  /** 暂存队列（worklet 未就绪 / 时钟未建立），最多 {@link UNMAPPED_HOLD_MS}。 */
  let held: HeldFrame[] = []

  /**
   * 已排队音频的**播完**时刻（墙钟毫秒）。null = 当前没有播放时间轴。
   *
   * 它同时是「环深」的来源：`endDueMs - now` 就是环里还压着多少毫秒的声音。
   */
  let endDueMs: number | null = null

  /**
   * 投递游标（输出帧号）：下一包最早可以在哪一帧起播。
   *
   * 为什么不能只靠 `ctx.currentTime` 推算：`AudioContext` 在 `suspended` 状态下
   * `currentTime` **不推进**（恒为 0），而解禁后到 `resume()` 真正生效之间有一个
   * 几十毫秒的窗口。这个窗口里若连投数包，每包都会算出同一个 `startDueFrame`，
   * 后一包直接覆盖前一包的起点 —— 表现为起播瞬间的咔哒声与丢音，且只在真机上出现。
   *
   * 游标只增不减，并在每次 `reanchor()` 时作废（新时间轴重新从当前帧算起）。
   */
  let nextFrameCursor = 0

  const stats: LiveAudioPlayerStats = {
    droppedStale: 0,
    droppedAhead: 0,
    rebased: 0,
    played: 0,
    held: 0,
    degraded: false,
  }

  function applyGain(): void {
    if (!gain || !ctx) return
    const target = muted ? 0 : volume
    try {
      // 极短时间常数：硬赋值在快速拖滑杆时会有咔哒声，长渐变又会让静音「拖尾」。
      gain.gain.setTargetAtTime(target, ctx.currentTime, 0.01)
    } catch {
      gain.gain.value = target
    }
  }

  /** 清空暂存队列（不计数）。 */
  function dropHeld(silent: boolean): void {
    if (silent && held.length > 0) stats.droppedStale += held.length
    held = []
    stats.held = 0
  }

  /**
   * 重锚：清空 worklet 环形缓冲，作废播放时间轴。
   *
   * 所有触发路径（DISCONTINUITY / 时间轴跳变 / 环深超限 / 外部 flush）都走这里，
   * 保证「重锚」只有一个实现，不会在不同路径上漂移出不同的预滚量。
   *
   * 首次锚定（`start()` 后第一包）**不**走这里：它没有旧时间轴要作废，也不该记一次重锚。
   * 但两者的「新时间轴起点」（`now + preRoll`）由同一个 `beginTimeline()` 决定。
   */
  function reanchor(): void {
    endDueMs = null
    nextFrameCursor = 0
    stats.rebased++
    // 暂存帧属于旧时间轴，重锚时一并作废（否则会以过期 pts 重新进入新时间轴）。
    dropHeld(true)
    if (node) {
      try {
        node.port.postMessage({ type: 'flush' })
      } catch {
        // 端口已关：忽略，节点即将被丢弃。
      }
    }
  }

  /** `AudioContext` 输出时间轴的当前帧号（worklet 里 `currentFrame` 的等价物）。 */
  function outputFrameNow(): number {
    return ctx ? Math.round(ctx.currentTime * outputRate) : 0
  }

  /**
   * 下一包的起播帧号：`ctx.currentTime` 与投递游标取较大者。
   *
   * 前者覆盖「ctx 正常推进」的稳态；后者覆盖 `suspended` 期间 `currentTime` 冻结、
   * 多包被挤到同一帧的窗口（见 {@link nextFrameCursor}）。
   */
  function reserveFramesAt(startDueMs: number): number {
    const fromClock =
      outputFrameNow() + Math.round(((startDueMs - now()) / 1000) * outputRate)
    return Math.max(0, fromClock, nextFrameCursor)
  }

  /** 把一帧真正交给 worklet，并从 `startDueMs` 开始播放。 */
  function deliver(
    pcm: Float32Array,
    inputRate: number,
    startDueMs: number,
    ptsUs: number,
    reanchored: boolean
  ): void {
    if (!node) return
    // startDueMs 是墙钟时刻，worklet 要的是**输出时间轴的绝对帧号**：
    // 相对偏移（startDueMs - now）折算成秒再加到当前帧上。
    const startDueFrame = reserveFramesAt(startDueMs)
    node.port.postMessage({
      type: 'push',
      samples: pcm,
      inputRate,
      startDueFrame,
    })
    // 游标推进到本包之后：下一包最早接在这里，绝不再回到同一帧。
    nextFrameCursor =
      startDueFrame + Math.round((pcm.length / inputRate) * outputRate)
    stats.played++
    options.onScheduled?.({
      ptsUs,
      startDueMs,
      nowMs: now(),
      reanchored,
    })
  }

  /** 清掉暂存队列里超时（> {@link UNMAPPED_HOLD_MS}）的帧。 */
  function expireHeld(wallMs: number): void {
    while (held.length > 0 && wallMs - held[0].heldAtMs > UNMAPPED_HOLD_MS) {
      held.shift()
      stats.droppedStale++
    }
    stats.held = held.length
  }

  /** 把暂存帧按给定时钟重新投递（保持相对顺序）。 */
  function drainHeld(clock: PresentationClockSnapshot): void {
    if (held.length === 0) return
    const pending = held
    held = []
    stats.held = 0
    for (const entry of pending) {
      schedule(entry.frame, entry.pcm, entry.inputRate, clock)
    }
  }

  /**
   * 建立一条**新的**播放时间轴：从 `now + preRoll` 起播本帧。
   *
   * 首包、时间轴跳变、环深超限三条路径都收敛到这里 —— 它们的差别只在于「旧时间轴
   * 怎么处理」，而新时间轴的起播点必须完全一致，否则三种情形下会得到不同的起播延迟。
   */
  function beginTimeline(
    pcm: Float32Array,
    inputRate: number,
    durationMs: number,
    wall: number,
    ptsUs: number
  ): void {
    const startDue = wall + PRE_ROLL_MS
    deliver(pcm, inputRate, startDue, ptsUs, true)
    endDueMs = startDue + durationMs
  }

  /** 时间轴已建立后的正常投递路径。 */
  function schedule(
    frame: AudioFrame,
    pcm: Float32Array,
    inputRate: number,
    clock: PresentationClockSnapshot
  ): void {
    const wall = now()
    const dueMs = audioDueMs(clock, frame.ptsUs)

    if (dueMs < wall - STALE_THRESHOLD_MS) {
      // 陈旧：按当前时钟这帧应当在 250 ms 前播完。现在投递只会被塞到已播位置之后，
      // 成为一段「回声」，比丢掉更糟。
      stats.droppedStale++
      return
    }

    const durationMs = (pcm.length / inputRate) * 1000

    if (dueMs > wall + FUTURE_THRESHOLD_MS) {
      // 时间轴跳到墙钟前面 400 ms 以上（切码流 / 解码突发 / 页面挂起后恢复）。
      // 继续投递只会让延迟单向累积，然后一次性爆掉。
      stats.droppedAhead++
      reanchor()
      beginTimeline(pcm, inputRate, durationMs, wall, frame.ptsUs)
      return
    }

    if (endDueMs === null) {
      // 首包（或刚重锚过）：没有播放位置可比较，直接以本帧起播。
      beginTimeline(pcm, inputRate, durationMs, wall, frame.ptsUs)
      return
    }

    if (endDueMs + durationMs > wall + RING_HIGH_WATER_MS) {
      // 环深超限：整段重锚而不是逐帧丢旧（理由见 RING_HIGH_WATER_MS 注释）。
      reanchor()
      beginTimeline(pcm, inputRate, durationMs, wall, frame.ptsUs)
      return
    }

    // 正常：接着上一帧的尾部排下去。已落后于墙钟（欠载已发生）就从现在另起，
    // 否则延迟会一帧一帧地累积下去。
    const startDue = Math.max(endDueMs, wall)
    deliver(pcm, inputRate, startDue, frame.ptsUs, false)
    endDueMs = startDue + durationMs
  }

  function pushFrame(
    frame: AudioFrame,
    pcm: Float32Array,
    inputRate: number,
    clock: PresentationClockSnapshot | null,
    fresh: boolean
  ): void {
    if (!(inputRate > 0) || pcm.length === 0) return

    if (fresh) {
      // 源流时间轴刚跳变（DISCONTINUITY）：旧时钟映射对新包已经没意义，
      // 直接以本包建立新时间轴 —— 既不看陈旧、也不看超前，否则重锚帧本身
      // 会被它触发的那次跳变判定成「陈旧」然后丢掉，等于永远等不到新起点。
      beginTimeline(
        pcm,
        inputRate,
        (pcm.length / inputRate) * 1000,
        now(),
        frame.ptsUs
      )
      return
    }

    if (!clock) {
      // 视频还没建立映射：只缓存 100 ms。缓存更久只会换来「解禁后先听到几秒前的
      // 声音」，听感比静音更糟（与 audio-channel 不缓存历史帧同一个取舍）。
      const wall = now()
      expireHeld(wall)
      held.push({ frame, pcm, inputRate, heldAtMs: wall })
      stats.held = held.length
      return
    }

    if (held.length > 0) drainHeld(clock)
    schedule(frame, pcm, inputRate, clock)
  }

  return {
    async start(): Promise<void> {
      if (closed) return
      if (ready) return ready

      ready = (async () => {
        try {
          const Ctor = globalThis.AudioContext
          if (typeof Ctor !== 'function') {
            stats.degraded = true
            return
          }

          // ⚠️ 触发 resume() 尝试唤醒渲染线程，但绝不 await 它（无用户手势时该 Promise 永不落地，await 会挂死，见 research §2.1）。
          const context = new Ctor()
          if (context.state === 'suspended') {
            void context.resume().catch(() => {})
          }
          if (
            !context.audioWorklet ||
            typeof globalThis.AudioWorkletNode !== 'function'
          ) {
            stats.degraded = true
            // 上下文已建但无法装配：立刻释放，避免留下一个永远 suspended 的空上下文。
            void context.close().catch(() => {})
            return
          }

          const url = URL.createObjectURL(
            new Blob([workletSource], { type: 'text/javascript' })
          )
          try {
            await context.audioWorklet.addModule(url)
          } finally {
            // addModule 已在解析阶段读完源码，可以立刻回收。
            URL.revokeObjectURL(url)
          }
          if (closed) {
            void context.close().catch(() => {})
            return
          }

          const workletNode = new AudioWorkletNode(
            context,
            AUDIO_WORKLET_PROCESSOR_NAME,
            {
              numberOfInputs: 0,
              numberOfOutputs: 1,
              outputChannelCount: [2],
            }
          )
          const gainNode = context.createGain()
          gainNode.gain.value = muted ? 0 : volume
          workletNode.connect(gainNode)
          gainNode.connect(context.destination)

          ctx = context
          node = workletNode
          gain = gainNode
          outputRate = context.sampleRate || FALLBACK_SAMPLE_RATE
          attached = true
          applyGain()

          if (context.state === 'suspended') {
            void context.resume().catch(() => {})
          }

          // start() 与首帧之间通常有几毫秒：把这期间攒下的帧补投。
          const clock = getClock()
          if (clock) drainHeld(clock)
        } catch {
          // 任何装配失败都降级为「无声」：上层只看到 stats.degraded，
          // 不黑屏、不弹错、不进报错循环 —— 视频必须继续。
          stats.degraded = true
          attached = false
          node = null
          gain = null
          ctx = null
          dropHeld(false)
        }
      })()

      return ready
    },

    push(frame: AudioFrame, pcm: Float32Array, inputRate: number): void {
      if (closed || stats.degraded) return

      if (ctx && ctx.state === 'suspended') {
        void ctx.resume().catch(() => {})
      }

      if (!attached) {
        // start() 还在等 addModule，或压根没 start。按时钟未建立的语义暂存 100 ms：
        // 解禁瞬间首包不允许因此丢失，但也不允许无限堆积。
        const wall = now()
        expireHeld(wall)
        held.push({ frame, pcm, inputRate, heldAtMs: wall })
        stats.held = held.length
        return
      }

      // 重锚三处之一：源流时间轴跳变。worklet 里的旧样本全部作废，
      // 并以本包为起点重建时间轴。
      const discontinuity = (frame.flags & AUDIO_FLAG_DISCONTINUITY) !== 0
      if (discontinuity) reanchor()

      pushFrame(frame, pcm, inputRate, getClock(), discontinuity)
    },

    flush(): void {
      if (closed) return
      reanchor()
    },

    setMuted(next: boolean): void {
      muted = next
      applyGain()
      if (!muted && ctx && ctx.state === 'suspended') {
        void ctx.resume().catch(() => {})
      }
    },

    setVolume(next: number): void {
      volume = clampVolume(next)
      applyGain()
    },

    async close(): Promise<void> {
      if (closed) return
      closed = true
      attached = false
      endDueMs = null
      dropHeld(false)

      const worklet = node
      const gainNode = gain
      const context = ctx
      node = null
      gain = null
      ctx = null
      ready = null

      try {
        worklet?.port.postMessage({ type: 'flush' })
        worklet?.disconnect()
        gainNode?.disconnect()
      } catch {
        // 已断开的节点再 disconnect 会抛；上下文本身还是要关。
      }

      if (context) {
        try {
          await context.close()
        } catch {
          // 已 closed 的上下文重复 close 会 reject，幂等语义下不算错误。
        }
      }
    },

    get stats() {
      return stats
    },
  }
}
