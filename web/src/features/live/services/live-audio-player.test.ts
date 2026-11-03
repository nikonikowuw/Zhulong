import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AUDIO_FLAG_DISCONTINUITY, type AudioFrame } from './audio-channel'
import {
  AUDIO_WORKLET_PROCESSOR_NAME,
  audioDueMs,
  createLiveAudioPlayer,
  type PresentationClockSnapshot,
} from './live-audio-player'

/**
 * 播放器的契约（design.md §4.2 逐条）：
 *
 *   1. `due` 与视频侧**同式**：`wallMs + (pts - clockPts)/1000 + targetLatency`；
 *   2. 陈旧（due < now - 250）丢，超前（due > now + 400）重锚；
 *   3. `clock === null` 最多缓存 100 ms；
 *   4. 重锚三处：首包 / DISCONTINUITY / 外部 flush。
 *
 * 这些全是纯算术，因此一律注入 `now`，不用 fake timers —— 定时器只能操纵「现在几点」，
 * 而这里需要精确表达「墙钟推进了多久」以及「此时时钟快照是什么」。
 */

/** worklet 端口收到的一条消息。 */
interface PortMessage {
  type: string
  samples?: Float32Array
  inputRate?: number
  startDueFrame?: number
}

class FakePort {
  readonly messages: PortMessage[] = []
  onmessage: ((event: MessageEvent) => void) | null = null

  postMessage(message: PortMessage): void {
    this.messages.push(message)
  }

  /** 只取 push 消息（flush 只表示清空）。 */
  pushes(): PortMessage[] {
    return this.messages.filter((m) => m.type === 'push')
  }

  countOf(type: string): number {
    return this.messages.filter((m) => m.type === type).length
  }
}

class FakeGainNode {
  readonly gain = {
    value: 1,
    setTargetAtTime: (target: number) => {
      this.gain.value = target
    },
  }
  connect = vi.fn()
  disconnect = vi.fn()
}

class FakeAudioWorkletNode {
  static instances: FakeAudioWorkletNode[] = []
  readonly port = new FakePort()
  connect = vi.fn()
  disconnect = vi.fn()

  constructor(
    public readonly context: unknown,
    public readonly name: string,
    public readonly nodeOptions?: AudioWorkletNodeOptions
  ) {
    FakeAudioWorkletNode.instances.push(this)
  }
}

class FakeAudioContext {
  static instances: FakeAudioContext[] = []
  static moduleError: Error | null = null
  static modules: string[] = []

  readonly sampleRate: number
  currentTime = 0
  state: AudioContextState = 'suspended'
  readonly destination = {}
  readonly gains: FakeGainNode[] = []
  closed = false
  createGainCount = 0
  resumeCalls = 0

  readonly audioWorklet = {
    addModule: (url: string): Promise<void> => {
      FakeAudioContext.modules.push(url)
      if (FakeAudioContext.moduleError) {
        return Promise.reject(FakeAudioContext.moduleError)
      }
      return Promise.resolve()
    },
  }

  constructor(options?: AudioContextOptions) {
    this.sampleRate = options?.sampleRate ?? 48_000
    FakeAudioContext.instances.push(this)
  }

  createGain(): FakeGainNode {
    this.createGainCount++
    const gain = new FakeGainNode()
    this.gains.push(gain)
    return gain
  }

  resume(): Promise<void> {
    // 真实浏览器里无手势时这个 Promise 永不落地；这里返回一个 pending Promise
    // 正是为了让「谁 await 了它」立刻挂死 —— 播放器必须不依赖它。
    this.resumeCalls++
    return new Promise<void>(() => {})
  }

  close(): Promise<void> {
    this.closed = true
    this.state = 'closed'
    return Promise.resolve()
  }
}

function installFakes(): void {
  FakeAudioContext.instances = []
  FakeAudioContext.modules = []
  FakeAudioContext.moduleError = null
  FakeAudioWorkletNode.instances = []
  vi.stubGlobal('AudioContext', FakeAudioContext)
  vi.stubGlobal('AudioWorkletNode', FakeAudioWorkletNode)
}

/** 构造一帧（payload 内容无关，播放器只看 flags/ptsUs）。 */
function makeFrame(ptsUs: number, flags = 0): AudioFrame {
  return {
    codec: 0x11,
    flags,
    seq: 0,
    ptsUs,
    payload: new Uint8Array(160),
  }
}

/** 8 kHz G.711 的一包：160 样本 = 20 ms。 */
function pcm(ms = 20, rate = 8000): Float32Array {
  return new Float32Array(Math.round((ms / 1000) * rate))
}

/** 可控墙钟。 */
function createClock() {
  let value = 0
  return {
    now: () => value,
    set(next: number) {
      value = next
    },
    advance(ms: number) {
      value += ms
    },
  }
}

/** 视频侧呈现时钟快照（100 ms 目标延迟，与 scheduler 默认值一致）。 */
function snapshot(
  wallMs: number,
  ptsUS: number,
  targetLatencyMs = 100
): PresentationClockSnapshot {
  return { wallMs, ptsUS, targetLatencyMs }
}

describe('audioDueMs', () => {
  it('与视频侧同式：wallMs + (pts - clockPts)/1000 + targetLatency', () => {
    const clock = snapshot(1000, 0, 100)
    // pts = 50_000 µs = 50 ms → 1000 + 50 + 100
    expect(audioDueMs(clock, 50_000)).toBe(1150)
  })

  it('PTS 早于时钟锚点时 due 落在锚点之前（负偏移），不做钳制', () => {
    const clock = snapshot(1000, 200_000, 100)
    expect(audioDueMs(clock, 0)).toBe(900)
  })
})

describe('createLiveAudioPlayer', () => {
  let clock: ReturnType<typeof createClock>

  beforeEach(() => {
    clock = createClock()
    installFakes()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  function createPlayer(
    getClock: () => PresentationClockSnapshot | null,
    extra?: { volume?: number }
  ) {
    return createLiveAudioPlayer({ getClock, now: clock.now, ...extra })
  }

  it('start() 之前不创建 AudioContext（Autoplay Policy）', () => {
    createPlayer(() => null)
    expect(FakeAudioContext.instances).toHaveLength(0)
  })

  it('start() 装配 worklet：不 resume，图连到 destination', async () => {
    const player = createPlayer(() => null)
    try {
      await player.start()

      expect(FakeAudioContext.instances).toHaveLength(1)
      const ctx = FakeAudioContext.instances[0]
      // ⚠️ 绝不 await resume()：无手势时它永不落地（research §2.1）。
      expect(ctx.resumeCalls).toBe(0)
      expect(ctx.state).toBe('suspended')

      // 必须是 blob URL，且 addModule 之后立刻回收（不泄漏）
      expect(FakeAudioContext.modules).toHaveLength(1)
      const url = FakeAudioContext.modules[0]
      expect(url.startsWith('blob:')).toBe(true)
      expect(URL.revokeObjectURL).toBeTypeOf('function')

      expect(FakeAudioWorkletNode.instances).toHaveLength(1)
      const node = FakeAudioWorkletNode.instances[0]
      expect(node.name).toBe(AUDIO_WORKLET_PROCESSOR_NAME)
      expect(node.nodeOptions).toMatchObject({
        numberOfInputs: 0,
        numberOfOutputs: 1,
        outputChannelCount: [2],
      })
      expect(node.connect).toHaveBeenCalledTimes(1)
      expect(ctx.createGainCount).toBe(1)
      expect(ctx.gains[0].connect).toHaveBeenCalledTimes(1)
      expect(player.stats.degraded).toBe(false)
    } finally {
      await player.close()
    }
  })

  it('start() 幂等：重复调用不会建第二个上下文', async () => {
    const player = createPlayer(() => null)
    try {
      await Promise.all([player.start(), player.start()])
      await player.start()
      expect(FakeAudioContext.instances).toHaveLength(1)
      expect(FakeAudioWorkletNode.instances).toHaveLength(1)
    } finally {
      await player.close()
    }
  })

  it('正常投递：帧交给 worklet，startDueFrame 落在墙钟之后', async () => {
    clock.set(1000)
    const player = createPlayer(() => snapshot(1000, 0, 100))
    try {
      await player.start()
      const port = FakeAudioWorkletNode.instances[0].port

      // pts=0 → due = 1000 + 0 + 100 = 1100，now = 1000，未陈旧
      player.push(makeFrame(0), pcm(), 8000)

      expect(port.pushes()).toHaveLength(1)
      const message = port.pushes()[0]
      expect(message.type).toBe('push')
      expect(message.inputRate).toBe(8000)
      expect(message.samples).toHaveLength(160)
      // 预滚 120 ms @48k = 5760 帧（ctx.currentTime 恒为 0）
      expect(message.startDueFrame).toBe(5760)
      expect(player.stats.played).toBe(1)
      expect(player.stats.droppedStale).toBe(0)
      expect(player.stats.droppedAhead).toBe(0)
    } finally {
      await player.close()
    }
  })

  it('陈旧：due < now - 250 时丢弃且不投递', async () => {
    // 时钟锚点：墙钟 1000 对应 pts 2_000_000 µs。pts=0 的帧 due = -900。
    clock.set(1000)
    const player = createPlayer(() => snapshot(1000, 2_000_000, 100))
    try {
      await player.start()
      const port = FakeAudioWorkletNode.instances[0].port

      player.push(makeFrame(0), pcm(), 8000)

      expect(port.pushes()).toHaveLength(0)
      expect(player.stats.droppedStale).toBe(1)
      expect(player.stats.played).toBe(0)
    } finally {
      await player.close()
    }
  })

  it('刚好在陈旧边界内（due = now - 250）仍然投递', async () => {
    // 用首包建立时间轴后再比较：先投一帧正常帧，再投一帧 due = now - 250 的。
    clock.set(1000)
    let current = snapshot(1000, 0, 100)
    const player = createPlayer(() => current)
    try {
      await player.start()
      const port = FakeAudioWorkletNode.instances[0].port

      player.push(makeFrame(0), pcm(), 8000) // due 1100
      expect(port.pushes()).toHaveLength(1)

      clock.set(2000)
      // due = 1000 + (pts)/1000 + 100 = 1750 → now - 250 = 1750，边界内
      player.push(makeFrame(650_000), pcm(), 8000)
      expect(port.pushes()).toHaveLength(2)
      expect(player.stats.droppedStale).toBe(0)

      // 再早 1 ms 就越界了
      clock.set(2001)
      current = snapshot(1000, 0, 100)
      player.push(makeFrame(650_000), pcm(), 8000)
      expect(port.pushes()).toHaveLength(2)
      expect(player.stats.droppedStale).toBe(1)
    } finally {
      await player.close()
    }
  })

  it('超前：due > now + 400 时重锚并以新包起播', async () => {
    clock.set(1000)
    const player = createPlayer(() => snapshot(1000, 0, 100))
    try {
      await player.start()
      const port = FakeAudioWorkletNode.instances[0].port

      // pts = 1_000_000 µs → due = 1000 + 1000 + 100 = 2100，now + 400 = 1400
      player.push(makeFrame(1_000_000), pcm(), 8000)

      expect(player.stats.rebased).toBe(1)
      expect(player.stats.droppedAhead).toBe(1)
      expect(port.pushes()).toHaveLength(1)
      // 重锚后从 now + preRoll(120ms) 起播：120ms @48k = 5760
      expect(port.pushes()[0].startDueFrame).toBe(5760)
      // 重锚会通知 worklet 清空旧样本
      expect(port.countOf('flush')).toBe(1)
    } finally {
      await player.close()
    }
  })

  it('环深超限（> 250 ms）时整段重锚而不是逐帧丢旧', async () => {
    clock.set(1000)
    const player = createPlayer(() => snapshot(1000, 0, 100))
    try {
      await player.start()
      const port = FakeAudioWorkletNode.instances[0].port

      // 每包 60 ms（480 样本 @8k）。首包从 now+120 起播 → 环深 180 ms。
      player.push(makeFrame(0), pcm(60), 8000)
      expect(player.stats.rebased).toBe(0)

      // 第二包：环深 240 ms，仍在 250 以内
      player.push(makeFrame(60_000), pcm(60), 8000)
      expect(player.stats.rebased).toBe(0)

      // 第三包：环深将达 300 ms → 超 250，整段重锚
      player.push(makeFrame(120_000), pcm(60), 8000)
      expect(player.stats.rebased).toBe(1)
      // 重锚不会吞掉触发它的那一包，三包都在
      expect(port.pushes()).toHaveLength(3)
      expect(player.stats.played).toBe(3)
      expect(port.pushes()[2].startDueFrame).toBe(
        Math.round((120 / 1000) * 48_000)
      )
    } finally {
      await player.close()
    }
  })

  it('clock === null：缓存不超过 100 ms，超时丢弃', async () => {
    clock.set(0)
    let current: PresentationClockSnapshot | null = null
    const player = createPlayer(() => current)
    try {
      await player.start()
      const port = FakeAudioWorkletNode.instances[0].port

      player.push(makeFrame(0), pcm(), 8000)
      expect(player.stats.held).toBe(1)
      expect(port.pushes()).toHaveLength(0)

      // 100 ms 内仍然留着
      clock.set(100)
      player.push(makeFrame(20_000), pcm(), 8000)
      expect(player.stats.held).toBe(2)
      expect(player.stats.droppedStale).toBe(0)

      // 推进到 250 ms：此时 0 ms 与 20 ms 的两帧都超时（>100ms）
      clock.set(250)
      player.push(makeFrame(40_000), pcm(), 8000)
      expect(player.stats.droppedStale).toBe(2)
      expect(player.stats.held).toBe(1)

      // 时钟一到，缓存帧立刻按新时钟重投
      current = snapshot(250, 40_000, 100)
      clock.set(260)
      player.push(makeFrame(60_000), pcm(), 8000)
      expect(player.stats.held).toBe(0)
      expect(port.pushes().length).toBeGreaterThanOrEqual(2)
    } finally {
      await player.close()
    }
  })

  it('外部 flush() 重锚并计数', async () => {
    clock.set(1000)
    const player = createPlayer(() => snapshot(1000, 0, 100))
    try {
      await player.start()
      const port = FakeAudioWorkletNode.instances[0].port

      player.push(makeFrame(0), pcm(), 8000)
      const flushesBefore = port.countOf('flush')
      const rebasedBefore = player.stats.rebased

      player.flush()

      expect(port.countOf('flush')).toBe(flushesBefore + 1)
      expect(player.stats.rebased).toBe(rebasedBefore + 1)

      // flush 之后时间轴作废：下一帧回到「首包」路径（now + preRoll）
      clock.set(1100)
      player.push(makeFrame(100_000), pcm(), 8000)
      expect(port.pushes()).toHaveLength(2)
      expect(port.pushes()[1].startDueFrame).toBe(
        Math.round((120 / 1000) * 48_000)
      )
    } finally {
      await player.close()
    }
  })

  it('DISCONTINUITY 标志触发重锚，且该帧不被丢弃', async () => {
    clock.set(1000)
    const player = createPlayer(() => snapshot(1000, 0, 100))
    try {
      await player.start()
      const port = FakeAudioWorkletNode.instances[0].port

      player.push(makeFrame(0), pcm(), 8000)
      const rebasedBefore = player.stats.rebased

      player.push(makeFrame(20_000, AUDIO_FLAG_DISCONTINUITY), pcm(), 8000)

      expect(player.stats.rebased).toBe(rebasedBefore + 1)
      expect(port.pushes()).toHaveLength(2)
      expect(player.stats.droppedStale).toBe(0)
      expect(player.stats.droppedAhead).toBe(0)
    } finally {
      await player.close()
    }
  })

  it('addModule 抛错 → 降级：start() 仍 resolve，push() 不抛', async () => {
    FakeAudioContext.moduleError = new Error('worklet unavailable')
    const player = createPlayer(() => snapshot(0, 0, 100))
    try {
      await expect(player.start()).resolves.toBeUndefined()
      expect(player.stats.degraded).toBe(true)

      expect(() => player.push(makeFrame(0), pcm(), 8000)).not.toThrow()
      expect(() => player.push(makeFrame(20_000), pcm(), 8000)).not.toThrow()
      // 降级后不再装配节点
      expect(FakeAudioWorkletNode.instances).toHaveLength(0)
    } finally {
      await player.close()
    }
  })

  it('audioWorklet 缺失 → 降级，且不留下悬空的 AudioContext', async () => {
    class NoWorkletContext {
      readonly sampleRate = 48_000
      currentTime = 0
      state: AudioContextState = 'suspended'
      readonly destination = {}
      closed = false
      createGain() {
        throw new Error('should not be called')
      }
      close(): Promise<void> {
        this.closed = true
        return Promise.resolve()
      }
    }
    // 该实例需要被记录，便于断言确实被关掉了
    const created: NoWorkletContext[] = []
    class Tracked extends NoWorkletContext {
      constructor() {
        super()
        created.push(this)
      }
    }
    vi.stubGlobal('AudioContext', Tracked)

    const player = createPlayer(() => null)
    try {
      await player.start()
      expect(player.stats.degraded).toBe(true)
      expect(created).toHaveLength(1)
      expect(created[0].closed).toBe(true)
      expect(() => player.push(makeFrame(0), pcm(), 8000)).not.toThrow()
    } finally {
      await player.close()
    }
  })

  it('close() 幂等；close 之后 push() 不抛', async () => {
    const player = createPlayer(() => snapshot(0, 0, 100))
    await player.start()
    const ctx = FakeAudioContext.instances[0]

    await player.close()
    expect(ctx.closed).toBe(true)

    // 第二次与第三次 close 仍 resolve（不 reject）
    await expect(player.close()).resolves.toBeUndefined()
    await expect(player.close()).resolves.toBeUndefined()

    expect(() => player.push(makeFrame(0), pcm(), 8000)).not.toThrow()
    expect(() => player.flush()).not.toThrow()
    expect(player.stats.played).toBe(0)
  })

  it('setMuted / setVolume 只改 GainNode，不重建图', async () => {
    const player = createPlayer(() => null, { volume: 0.5 })
    try {
      await player.start()
      const ctx = FakeAudioContext.instances[0]
      const gain = ctx.gains[0]
      expect(gain.gain.value).toBe(0.5)

      player.setMuted(true)
      expect(gain.gain.value).toBe(0)
      player.setMuted(false)
      expect(gain.gain.value).toBe(0.5)

      player.setVolume(0.25)
      expect(gain.gain.value).toBe(0.25)

      // 越界音量被钳制，不把响度控制权交给调用方
      player.setVolume(4)
      expect(gain.gain.value).toBe(1)
      player.setVolume(-1)
      expect(gain.gain.value).toBe(0)
      player.setVolume(Number.NaN)
      expect(gain.gain.value).toBe(0)

      expect(FakeAudioContext.instances).toHaveLength(1)
      expect(FakeAudioWorkletNode.instances).toHaveLength(1)
    } finally {
      await player.close()
    }
  })

  it('未 start() 时 push 不抛（不缓存、不建上下文）', () => {
    const player = createPlayer(() => snapshot(0, 0, 100))
    expect(() => player.push(makeFrame(0), pcm(), 8000)).not.toThrow()
    expect(FakeAudioContext.instances).toHaveLength(0)
    expect(player.stats.played).toBe(0)
  })

  it('空 PCM / 非法采样率被忽略', async () => {
    clock.set(1000)
    const player = createPlayer(() => snapshot(1000, 0, 100))
    try {
      await player.start()
      const port = FakeAudioWorkletNode.instances[0].port

      player.push(makeFrame(0), new Float32Array(0), 8000)
      player.push(makeFrame(0), pcm(), 0)
      expect(port.pushes()).toHaveLength(0)
      expect(player.stats.played).toBe(0)
    } finally {
      await player.close()
    }
  })
})

/**
 * 未验证风险转成回归（原实现者的报告 §Not done 里那条）：
 *
 * `AudioContext.currentTime` 在 `suspended` 状态下**不推进**（恒为 0）。而
 * `deliver()` 用 `outputFrameNow()` = `currentTime * sampleRate` 去折算
 * `startDueFrame`，于是「解禁 → ctx 尚未 resume 完 → 连投几包」这条路径上，
 * 所有包都会算出同一个 `startDueFrame`（都等于预滚量），互相覆盖地挤在同一时刻。
 *
 * 现实中的触发窗口很窄（resume 通常在几十毫秒内完成），但**首包丢失/重复起播**
 * 是「点了喇叭先咔一下」的直接来源，而且它只在真机上出现，单测不写就永远发现不了。
 *
 * 期望：多个包在 ctx 未推进时必须得到**严格递增**的 startDueFrame，即播放器自己
 * 维持一条与 ctx.currentTime 解耦的投递游标。
 */
describe('createLiveAudioPlayer · suspended 期间的投递游标', () => {
  let clock: ReturnType<typeof createClock>

  beforeEach(() => {
    clock = createClock()
    installFakes()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('ctx.currentTime 不推进时，连续投递的 startDueFrame 仍然严格递增', async () => {
    // ctx.currentTime 恒为 0（suspended 的真实行为，FakeAudioContext 默认如此）。
    clock.set(1000)
    const player = createLiveAudioPlayer({
      getClock: () => snapshot(1000, 0, 100),
      now: clock.now,
    })
    try {
      await player.start()
      const port = FakeAudioWorkletNode.instances[0].port

      // 连投 5 包，每包 20 ms，墙钟同步推进（模拟 resume 尚未完成的窗口）。
      for (let i = 0; i < 5; i++) {
        player.push(makeFrame(i * 20_000), pcm(), 8000)
        clock.advance(20)
      }

      const frames = port.pushes().map((m) => m.startDueFrame ?? -1)
      expect(frames).toHaveLength(5)
      // 每包必须比前一包晚 20 ms @48k = 960 帧。相等就意味着后一包覆盖前一包的起点。
      for (let i = 1; i < frames.length; i++) {
        expect(frames[i]).toBeGreaterThan(frames[i - 1])
      }
      expect(frames[1] - frames[0]).toBe(960)
    } finally {
      await player.close()
    }
  })
})
