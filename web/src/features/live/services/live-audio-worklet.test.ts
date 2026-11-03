import { afterEach, describe, expect, it } from 'vitest'
import workletSource from './live-audio-worklet.js?raw'

/**
 * worklet 的单元测试。
 *
 * worklet 跑在音频线程，浏览器测试拿不到它的 `process()`，但**它的逻辑全是纯算术**：
 * 环形缓冲、线性重采样、预滚静音、欠载淡入。因此这里把 `?raw` 源文本在**主线程**用
 * `new Function` 跑一遍，用假的 `AudioWorkletProcessor` / `registerProcessor` 与
 * `sampleRate` / `currentFrame` 全局提供 worklet 环境。
 *
 * 这样做的价值：
 *   1. 重采样比例写反（8k→48k 写成 ×6 而不是 ÷6）会立刻表现为「1 秒音频只够播 1/36 秒」；
 *   2. 欠载时如果跳读（`readTotal` 照常推进），输出会变成降调/跳段，而不是干净的静音；
 *   3. 淡入如果没做，恢复出声的第一帧就是满幅 —— 咔哒声的来源。
 *
 * ⚠️ 不用真实 `AudioContext` + 真 worklet：那需要用户手势，且时序不可断言（research §2.1）。
 */

const SAMPLE_RATE = 48_000
/** 10 ms 淡入 = 480 帧。 */
const FADE_FRAMES = Math.round(SAMPLE_RATE * 0.01)

interface Processor {
  port: { onmessage: ((event: MessageEvent) => void) | null }
  process: (inputs: Float32Array[][], outputs: Float32Array[][]) => boolean
}

type ProcessorCtor = new () => Processor

/** 每个用例独立装载一份 worklet（模块级状态：注册表、全局 sampleRate）。 */
function loadWorklet() {
  const registered = new Map<string, ProcessorCtor>()
  const globals = { sampleRate: SAMPLE_RATE, currentFrame: 0 }

  class FakeAudioWorkletProcessor {
    readonly port: { onmessage: ((event: MessageEvent) => void) | null } = {
      onmessage: null,
    }
  }

  // worklet 里的 `sampleRate` / `currentFrame` 是**全局**且必须可变，
  // 用 globalThis 上的访问器提供（不能按值传参，否则会被冻结在装载那一刻）。
  for (const key of ['sampleRate', 'currentFrame'] as const) {
    Object.defineProperty(globalThis, key, {
      configurable: true,
      get: () => globals[key],
    })
  }

  // biome-ignore lint: 在测试里执行 worklet 源文本是本用例的核心手法
  const install = new Function(
    'AudioWorkletProcessor',
    'registerProcessor',
    workletSource
  )
  install.call(
    undefined,
    FakeAudioWorkletProcessor,
    (name: string, ctor: ProcessorCtor) => {
      registered.set(name, ctor)
    }
  )

  const [name, ctor] = [...registered.entries()][0] ?? []
  if (!name || !ctor) throw new Error('worklet 未注册任何 processor')

  return {
    name,
    ctor,
    globals,
    create: (): Processor => new ctor(),
  }
}

/** 跑 `frames` 帧，断言双声道一致，返回左声道。 */
function render(processor: Processor, frames: number): number[] {
  const left = new Float32Array(frames)
  const right = new Float32Array(frames)
  processor.process([], [[left, right]])
  for (let i = 0; i < frames; i++) {
    if (right[i] !== left[i]) {
      throw new Error(`声道不一致 @${i}: L=${left[i]} R=${right[i]}`)
    }
  }
  return Array.from(left)
}

function send(processor: Processor, data: unknown): void {
  processor.port.onmessage?.({ data } as MessageEvent)
}

function push(
  processor: Processor,
  samples: Float32Array,
  inputRate: number,
  startDueFrame: number
): void {
  send(processor, { type: 'push', samples, inputRate, startDueFrame })
}

describe('live-audio-worklet', () => {
  afterEach(() => {
    delete (globalThis as { sampleRate?: number }).sampleRate
    delete (globalThis as { currentFrame?: number }).currentFrame
  })

  it('注册名与 TS 侧的 AUDIO_WORKLET_PROCESSOR_NAME 逐字一致', async () => {
    const { AUDIO_WORKLET_PROCESSOR_NAME } = await import('./live-audio-player')
    const { name } = loadWorklet()
    // 两处字符串不一致时 new AudioWorkletNode() 只会给一个 InvalidStateError，
    // 现场排查要花很久，这里直接钉死。
    expect(name).toBe(AUDIO_WORKLET_PROCESSOR_NAME)
    expect(workletSource).toContain(
      `const PROCESSOR_NAME = '${AUDIO_WORKLET_PROCESSOR_NAME}'`
    )
  })

  it('8k → 48k 线性重采样：1 秒输入正好铺满 1 秒输出', () => {
    const { create, globals } = loadWorklet()
    const processor = create()
    globals.currentFrame = 0

    // 恒定 0.5 的 1.5 秒 8k 音频（12000 样本）：多给半秒，避免断在缓冲末尾
    push(processor, new Float32Array(12_000).fill(0.5), 8000, 0)

    // 1 秒输出 = 48000 帧。比例若弄反（×6 而不是 ÷6），大半段会是静音。
    const total = 48_000
    const out: number[] = []
    for (let offset = 0; offset < total; offset += 128) {
      out.push(...render(processor, 128))
    }
    expect(out).toHaveLength(total)

    // 淡入之后是稳态 0.5，且一直维持到第 1 秒末尾（说明没有提前播完）
    expect(out[FADE_FRAMES]).toBeCloseTo(0.5, 5)
    const tail = out.slice(total - 200)
    expect(tail.every((v) => Math.abs(v - 0.5) < 0.01)).toBe(true)
  })

  it('48k 输入按 1:1 通过（不做多余插值）', () => {
    const { create, globals } = loadWorklet()
    const processor = create()
    globals.currentFrame = 0

    const ramp = new Float32Array(1000)
    for (let i = 0; i < ramp.length; i++) ramp[i] = i / 1000
    push(processor, ramp, 48_000, 0)

    // 一次跑够淡入区间 + 余量，才能检查淡入之后的 1:1 传递
    const out = render(processor, FADE_FRAMES + 200)
    // 前 480 帧淡入，之后应当逐样本等于输入的斜坡
    expect(out[FADE_FRAMES]).toBeCloseTo(ramp[FADE_FRAMES], 5)
    expect(out[600]).toBeCloseTo(ramp[600], 5)
  })

  it('欠载后输出静音（不跳读、不重复旧样本）', () => {
    const { create, globals } = loadWorklet()
    const processor = create()
    globals.currentFrame = 0

    // 100 ms（800 样本 @8k）→ 只够 4800 输出帧
    push(processor, new Float32Array(800).fill(0.5), 8000, 0)

    const out: number[] = []
    for (let i = 0; i < 60; i++) out.push(...render(processor, 128)) // 7680 帧

    expect(out[1000]).toBeCloseTo(0.5, 5)
    // 4800 帧之后缓冲已空：必须是干净的 0，而不是把旧样本循环播放。
    expect(out.slice(5200).every((v) => v === 0)).toBe(true)
  })

  it('首包按 startDueFrame 做预滚静音', () => {
    const { create, globals } = loadWorklet()
    const processor = create()
    globals.currentFrame = 0

    // 128 帧预滚（≈2.7 ms）
    push(processor, new Float32Array(800).fill(0.5), 8000, 128)

    const first = render(processor, 128)
    expect(first.every((v) => v === 0)).toBe(true)

    // 预滚结束后开始出声，并且先淡入（第 1 帧仍为 0）
    const second = render(processor, 128)
    expect(second[0]).toBe(0)
    expect(second[1]).toBeGreaterThan(0)
    expect(second[127]).toBeGreaterThan(second[1])
  })

  it('欠载恢复后重新淡入（避免硬切咔哒声）', () => {
    const { create, globals } = loadWorklet()
    const processor = create()
    globals.currentFrame = 0

    push(processor, new Float32Array(800).fill(0.5), 8000, 0)
    for (let i = 0; i < 40; i++) render(processor, 128) // 5120 帧 > 4800，已欠载

    // 补上下一包：startDueFrame 落后于当前帧 → 立刻起播
    globals.currentFrame = 5120
    push(processor, new Float32Array(800).fill(0.5), 8000, 5120)

    // 一次跑够淡入区间，才能看到「从 0 涨到接近 0.5」这条斜坡
    const resumed = render(processor, FADE_FRAMES + 100)
    // 恢复的第一个样本必须是 0（淡入起点），而不是直接 0.5
    expect(resumed[0]).toBe(0)
    expect(resumed[FADE_FRAMES / 2]).toBeGreaterThan(0)
    expect(resumed[FADE_FRAMES / 2]).toBeLessThan(0.5)
    expect(resumed[FADE_FRAMES + 99]).toBeCloseTo(0.5, 5)
  })

  it('flush 清空缓冲：之后不再出声', () => {
    const { create, globals } = loadWorklet()
    const processor = create()
    globals.currentFrame = 0

    push(processor, new Float32Array(800).fill(0.5), 8000, 0)
    expect(render(processor, 1024).some((v) => v !== 0)).toBe(true)

    send(processor, { type: 'flush' })

    expect(render(processor, 1024).every((v) => v === 0)).toBe(true)
  })

  it('采样率切换（切码流 8k → 48k）自动按重锚处理', () => {
    const { create, globals } = loadWorklet()
    const processor = create()
    globals.currentFrame = 0

    push(processor, new Float32Array(800).fill(0.5), 8000, 0)
    render(processor, 128)

    // 切到 48k：旧缓冲必须先清掉，否则会拿 8k 的残留按 1:1 播
    push(processor, new Float32Array(4800).fill(-0.5), 48_000, 0)
    const out = render(processor, 3000)

    // 淡入后应当是 -0.5（新流的样本），不该出现 +0.5 的旧残留
    expect(out[FADE_FRAMES + 1]).toBeCloseTo(-0.5, 5)
    expect(out.slice(1).every((v) => v <= 0)).toBe(true)
  })

  it('缓冲溢出丢最旧样本，但不影响继续出声', () => {
    const { create, globals } = loadWorklet()
    const processor = create()
    globals.currentFrame = 0

    // 容量 = 2 秒 @48k = 96000 样本；这里推 3 秒（24000 个 8k 样本）
    push(processor, new Float32Array(24_000).fill(0.5), 8000, 0)

    const out: number[] = []
    for (let i = 0; i < 400; i++) out.push(...render(processor, 128))

    // 溢出只丢掉最早的约 1 秒内容，不影响「仍在正常出声」
    expect(out[out.length - 1]).toBeCloseTo(0.5, 5)
    expect(out.slice(FADE_FRAMES).every((v) => Math.abs(v - 0.5) < 0.01)).toBe(
      true
    )
  })

  it('空 samples / 畸形消息被忽略，不抛', () => {
    const { create } = loadWorklet()
    const processor = create()

    expect(() => push(processor, new Float32Array(0), 8000, 0)).not.toThrow()
    expect(() => send(processor, { type: 'unknown' })).not.toThrow()
    expect(() => send(processor, null)).not.toThrow()
    expect(() => send(processor, {})).not.toThrow()
    expect(render(processor, 128).every((v) => v === 0)).toBe(true)
  })
})
