import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  AUDIO_CODEC_AAC,
  AUDIO_CODEC_PCMA,
  AUDIO_FLAG_DISCONTINUITY,
  type AudioChannelInfo,
  type AudioFrame,
} from './audio-channel'
import {
  createAudioFrameDecoder,
  type AudioDegradeReason,
} from './audio-frame-decoder'
import { type PresentationClockSnapshot } from './live-audio-player'

/**
 * 「帧 → PCM」分派器的契约。
 *
 * 这里 stub 掉 `AudioDecoder` / `EncodedAudioChunk`：真实解码器由浏览器提供，单测能
 * 断言的只有**我们把什么交给了它**（configure 的 description、chunk 的 timestamp、
 * AudioData 有没有 close）。解码结果对不对是浏览器的事，测它等于测 Chromium。
 */

interface PushedFrame {
  frame: AudioFrame
  pcm: Float32Array
  inputRate: number
}

class FakePlayer {
  readonly pushes: PushedFrame[] = []
  flushCalls = 0

  push(frame: AudioFrame, pcm: Float32Array, inputRate: number): void {
    this.pushes.push({ frame, pcm, inputRate })
  }

  flush(): void {
    this.flushCalls++
  }
}

/** 假 AudioData：只实现 `extractMono()` 用到的四个成员。 */
class FakeAudioData {
  closed = false
  readonly numberOfFrames: number
  readonly numberOfChannels: number
  readonly sampleRate: number
  readonly timestamp: number
  private readonly planes: Float32Array[]

  constructor(options: {
    frames: number
    channels: number
    sampleRate: number
    timestamp: number
    fill?: (channel: number) => number
  }) {
    this.numberOfFrames = options.frames
    this.numberOfChannels = options.channels
    this.sampleRate = options.sampleRate
    this.timestamp = options.timestamp
    this.planes = Array.from({ length: options.channels }, (_, channel) => {
      const value = options.fill ? options.fill(channel) : 0.25
      return new Float32Array(options.frames).fill(value)
    })
  }

  copyTo(destination: Float32Array, options: AudioDataCopyToOptions): void {
    if (this.closed) throw new Error('AudioData already closed')
    const plane = this.planes[options.planeIndex ?? 0]
    if (!plane) throw new Error(`no plane ${options.planeIndex}`)
    destination.set(plane.subarray(0, destination.length))
  }

  close(): void {
    if (this.closed) throw new Error('AudioData already closed')
    this.closed = true
  }
}

interface FakeDecoderState {
  instances: FakeDecoderState[]
  configs: AudioDecoderConfig[]
  supportResult: boolean | Error
  chunks: EncodedAudioChunkInit[]
  /** 构造时注册的 output 回调（测试里手动触发，模拟异步解码）。 */
  output: ((data: FakeAudioData) => void) | null
  /** 构造时注册的 error 回调。 */
  error: ((error: DOMException) => void) | null
}

function installDecoderStub(): FakeDecoderState {
  const state: FakeDecoderState = {
    instances: [],
    configs: [],
    supportResult: true,
    chunks: [],
    output: null,
    error: null,
  }
  state.instances.push(state)

  class StubAudioDecoder {
    state: 'unconfigured' | 'configured' | 'closed' = 'unconfigured'
    decodeQueueSize = 0

    constructor(init: AudioDecoderInit) {
      state.output = init.output as unknown as (data: FakeAudioData) => void
      state.error = init.error
    }

    static isConfigSupported(
      config: AudioDecoderConfig
    ): Promise<AudioDecoderSupport> {
      state.configs.push(config)
      if (state.supportResult instanceof Error) {
        return Promise.reject(state.supportResult)
      }
      return Promise.resolve({ supported: state.supportResult, config })
    }

    configure(): void {
      this.state = 'configured'
    }

    decode(chunk: EncodedAudioChunkInit): void {
      state.chunks.push(chunk)
    }

    close(): void {
      this.state = 'closed'
    }
  }

  class StubEncodedAudioChunk {
    constructor(init: EncodedAudioChunkInit) {
      Object.assign(this, init)
    }
  }

  vi.stubGlobal('AudioDecoder', StubAudioDecoder)
  vi.stubGlobal('EncodedAudioChunk', StubEncodedAudioChunk)
  return state
}

function makeFrame(overrides: Partial<AudioFrame> = {}): AudioFrame {
  return {
    codec: AUDIO_CODEC_PCMA,
    flags: 0,
    seq: 1,
    ptsUs: 1_000_000,
    payload: new Uint8Array(160).fill(0xd5),
    ...overrides,
  }
}

/** AAC-LC 8kHz 单声道 ASC 的 base64（`0x15 0x88`）。 */
const ASC_8K_MONO = 'FYg='

function aacInfo(overrides: Partial<AudioChannelInfo> = {}): AudioChannelInfo {
  return {
    codec: 'aac',
    sampleRate: 8000,
    channels: 1,
    config: ASC_8K_MONO,
    ...overrides,
  }
}

describe('createAudioFrameDecoder', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('G.711 A-law 帧 → PCM 长度与输入一致，采样率 8000', async () => {
    const player = new FakePlayer()
    const decoder = await createAudioFrameDecoder({
      info: { codec: 'pcma', sampleRate: 8000, channels: 1, config: '' },
      player,
    })

    expect(decoder.degraded).toBe(false)
    decoder.push(makeFrame({ payload: new Uint8Array(160) }))

    expect(player.pushes).toHaveLength(1)
    expect(player.pushes[0].pcm).toHaveLength(160)
    expect(player.pushes[0].inputRate).toBe(8000)
    expect(decoder.stats.decoded).toBe(1)
  })

  it('G.711 μ-law 帧 → PCM 长度与输入一致', async () => {
    const player = new FakePlayer()
    const decoder = await createAudioFrameDecoder({
      info: { codec: 'pcmu', sampleRate: 8000, channels: 1, config: '' },
      player,
    })

    decoder.push(makeFrame({ payload: new Uint8Array(320) }))
    expect(player.pushes[0].pcm).toHaveLength(320)
  })

  it('AAC：configure 收到 ASC 推导的 codec / 采样率 / 声道 / description', async () => {
    const state = installDecoderStub()
    const player = new FakePlayer()
    const decoder = await createAudioFrameDecoder({
      // ⚠️ info 里的 sampleRate/channels 故意写错：ASC 才是权威。
      info: aacInfo({ sampleRate: 90000, channels: 2 }),
      player,
    })

    expect(decoder.degraded).toBe(false)
    expect(state.configs).toHaveLength(1)
    expect(state.configs[0].codec).toBe('mp4a.40.2')
    expect(state.configs[0].sampleRate).toBe(8000)
    expect(state.configs[0].numberOfChannels).toBe(1)
    const description = state.configs[0].description
    expect(description).toBeInstanceOf(Uint8Array)
    expect(Array.from(description as Uint8Array)).toEqual([0x15, 0x88])
  })

  it('AAC：帧 → EncodedAudioChunk（key / pts / data）', async () => {
    const state = installDecoderStub()
    const player = new FakePlayer()
    const decoder = await createAudioFrameDecoder({
      info: aacInfo(),
      player,
    })

    const payload = new Uint8Array([0x21, 0x10, 0x05])
    decoder.push(
      makeFrame({ codec: AUDIO_CODEC_AAC, ptsUs: 2_500_000, payload })
    )

    expect(state.chunks).toHaveLength(1)
    expect(state.chunks[0].type).toBe('key')
    expect(state.chunks[0].timestamp).toBe(2_500_000)
    // 1024 样本 @ 8 kHz = 128000 µs
    expect(state.chunks[0].duration).toBe(128_000)
    expect(Array.from(state.chunks[0].data as Uint8Array)).toEqual([
      0x21, 0x10, 0x05,
    ])
  })

  it('AAC：output 把 AudioData 拷成单声道 PCM 并且**必须** close', async () => {
    const state = installDecoderStub()
    const player = new FakePlayer()
    await createAudioFrameDecoder({ info: aacInfo(), player })

    const data = new FakeAudioData({
      frames: 4,
      channels: 1,
      sampleRate: 8000,
      timestamp: 3_000_000,
      fill: () => 0.5,
    })
    state.output?.(data)

    expect(data.closed).toBe(true)
    expect(player.pushes).toHaveLength(1)
    expect(Array.from(player.pushes[0].pcm)).toEqual([0.5, 0.5, 0.5, 0.5])
    expect(player.pushes[0].frame.ptsUs).toBe(3_000_000)
    expect(player.pushes[0].inputRate).toBe(8000)
  })

  it('AAC：双声道按平均下混', async () => {
    const state = installDecoderStub()
    const player = new FakePlayer()
    await createAudioFrameDecoder({
      info: aacInfo({ config: 'EZA=', sampleRate: 48000, channels: 2 }),
      player,
    })

    const data = new FakeAudioData({
      frames: 3,
      channels: 2,
      sampleRate: 48000,
      timestamp: 0,
      fill: (channel) => (channel === 0 ? 1 : 0),
    })
    state.output?.(data)

    expect(Array.from(player.pushes[0].pcm)).toEqual([0.5, 0.5, 0.5])
    expect(data.closed).toBe(true)
  })

  it('ASC 缺失（config 为空）→ 降级，不抛、不配解码器', async () => {
    const state = installDecoderStub()
    const player = new FakePlayer()
    const degraded: AudioDegradeReason[] = []

    const decoder = await createAudioFrameDecoder({
      info: aacInfo({ config: '' }),
      player,
      onDegrade: (reason) => degraded.push(reason),
    })

    expect(decoder.degraded).toBe(true)
    expect(decoder.reason).toBe('unsupported_config')
    expect(degraded).toEqual(['unsupported_config'])
    // 一次 configure 都不该发生
    expect(state.configs).toHaveLength(0)

    // 降级后 push 是空操作（不抛）
    expect(() =>
      decoder.push(makeFrame({ codec: AUDIO_CODEC_AAC }))
    ).not.toThrow()
    expect(player.pushes).toHaveLength(0)
    expect(decoder.stats.dropped).toBe(1)
  })

  it('isConfigSupported 返回 false → 降级', async () => {
    const state = installDecoderStub()
    state.supportResult = false
    const player = new FakePlayer()
    const degraded: AudioDegradeReason[] = []

    const decoder = await createAudioFrameDecoder({
      info: aacInfo(),
      player,
      onDegrade: (reason) => degraded.push(reason),
    })

    expect(decoder.degraded).toBe(true)
    expect(degraded).toEqual(['unsupported_config'])
  })

  it('isConfigSupported reject → 降级（不抛到调用方）', async () => {
    const state = installDecoderStub()
    state.supportResult = new TypeError('bad config')
    const player = new FakePlayer()

    const decoder = await createAudioFrameDecoder({
      info: aacInfo(),
      player,
    })
    expect(decoder.degraded).toBe(true)
    expect(decoder.reason).toBe('unsupported_config')
  })

  it('环境无 AudioDecoder → 降级为 no_decoder', async () => {
    vi.stubGlobal('AudioDecoder', undefined)
    const player = new FakePlayer()
    const degraded: AudioDegradeReason[] = []

    const decoder = await createAudioFrameDecoder({
      info: aacInfo(),
      player,
      onDegrade: (reason) => degraded.push(reason),
    })

    expect(decoder.degraded).toBe(true)
    expect(degraded).toEqual(['no_decoder'])
  })

  it('解码器 error 回调 → 降级为 decoder_error，后续帧被丢弃', async () => {
    const state = installDecoderStub()
    const player = new FakePlayer()
    const onDegrade = vi.fn()

    const decoder = await createAudioFrameDecoder({
      info: aacInfo(),
      player,
      onDegrade,
    })

    // 真实解码器遇到坏码流时就是走 error 回调（不是把异常抛给 decode()）。
    state.error?.(new DOMException('bad stream', 'EncodingError'))

    expect(decoder.degraded).toBe(true)
    expect(decoder.reason).toBe('decoder_error')
    expect(onDegrade).toHaveBeenCalledTimes(1)

    decoder.push(makeFrame({ codec: AUDIO_CODEC_AAC }))
    expect(state.chunks).toHaveLength(0)
    expect(player.pushes).toHaveLength(0)
  })

  it('AAC 的 DISCONTINUITY 帧会先 flush 播放器', async () => {
    installDecoderStub()
    const player = new FakePlayer()
    const decoder = await createAudioFrameDecoder({ info: aacInfo(), player })

    decoder.push(
      makeFrame({ codec: AUDIO_CODEC_AAC, flags: AUDIO_FLAG_DISCONTINUITY })
    )
    expect(player.flushCalls).toBe(1)

    decoder.push(makeFrame({ codec: AUDIO_CODEC_AAC, flags: 0 }))
    expect(player.flushCalls).toBe(1)
  })

  it('close() 幂等，之后 push 是空操作', async () => {
    installDecoderStub()
    const player = new FakePlayer()
    const decoder = await createAudioFrameDecoder({ info: aacInfo(), player })

    decoder.close()
    expect(() => decoder.close()).not.toThrow()
    decoder.push(makeFrame({ codec: AUDIO_CODEC_AAC }))
    expect(player.pushes).toHaveLength(0)
  })
})

describe('audioDueMs（与视频同一时间轴）', () => {
  it('逐字同式：wallMs + (pts - clockPts)/1000 + targetLatency', async () => {
    const { audioDueMs } = await import('./live-audio-player')
    const clock: PresentationClockSnapshot = {
      wallMs: 1000,
      ptsUS: 0,
      targetLatencyMs: 100,
    }
    expect(audioDueMs(clock, 50_000)).toBe(1150)
  })
})
