import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  AUDIO_CODEC_PCMA,
  AUDIO_WIRE_HEADER_SIZE,
  AUDIO_WIRE_MAGIC,
  type AudioChannelHandlers,
  type AudioFrame,
  connectAudioChannel,
} from './audio-channel'

/**
 * 可控的假 WebSocket。
 *
 * 这里**不**连真后端：音频通道的全部价值在「收到什么帧，做什么状态迁移」，
 * 而真 WS 既测不出 magic 错误、也测不出 4404 这种只在特定后端状态下才出现的关闭码。
 * 用假 socket 可以把每条分支都拨一遍，且完全确定。
 */
class FakeWebSocket {
  static instances: FakeWebSocket[] = []

  static get last(): FakeWebSocket {
    const socket = FakeWebSocket.instances[FakeWebSocket.instances.length - 1]
    if (!socket) throw new Error('no FakeWebSocket instantiated')
    return socket
  }

  static reset(): void {
    FakeWebSocket.instances = []
  }

  readonly url: string
  binaryType = 'blob'
  readyState = 0 // CONNECTING
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  closeCalls = 0

  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }

  close(): void {
    this.closeCalls++
    this.readyState = 3 // CLOSED
  }

  /** 模拟服务端下发文本帧。 */
  emitText(data: string): void {
    this.onmessage?.({ data } as MessageEvent)
  }

  /** 模拟服务端下发二进制帧。 */
  emitBinary(data: ArrayBuffer): void {
    this.onmessage?.({ data } as MessageEvent)
  }

  /** 模拟连接关闭（真实浏览器里 onclose 一定异步到达）。 */
  emitClose(code: number): void {
    this.readyState = 3
    this.onclose?.({ code } as CloseEvent)
  }
}

function handlersStub() {
  return {
    onInfo: vi.fn<AudioChannelHandlers['onInfo']>(),
    onFrame: vi.fn<AudioChannelHandlers['onFrame']>(),
    onFatal: vi.fn<AudioChannelHandlers['onFatal']>(),
    onDisconnect: vi.fn<(code: number) => void>(),
  }
}

/** 按 ZLA1 头布局打包一帧：20 字节大端头 + 裸负载。 */
function buildAudioFrame({
  magic = AUDIO_WIRE_MAGIC,
  codec = AUDIO_CODEC_PCMA,
  flags = 0,
  seq = 1,
  ptsUs = 1_000_000,
  payload = new Uint8Array([0xd5, 0x55, 0xaa, 0x2a]),
}: {
  magic?: number
  codec?: number
  flags?: number
  seq?: number
  ptsUs?: number
  payload?: Uint8Array
} = {}): ArrayBuffer {
  const buffer = new ArrayBuffer(AUDIO_WIRE_HEADER_SIZE + payload.length)
  const view = new DataView(buffer)
  view.setUint32(0, magic, false)
  view.setUint8(4, codec)
  view.setUint8(5, flags)
  // 6..7 reserved
  view.setUint32(8, seq, false)
  view.setBigInt64(12, BigInt(ptsUs), false)
  new Uint8Array(buffer).set(payload, AUDIO_WIRE_HEADER_SIZE)
  return buffer
}

describe('connectAudioChannel', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    FakeWebSocket.reset()
    vi.stubGlobal('WebSocket', FakeWebSocket)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('连到该相机的音频 WS 端点，并把 binaryType 设为 arraybuffer', () => {
    const handlers = handlersStub()
    const channel = connectAudioChannel(
      { cameraId: 'cam-01', role: 'main' },
      handlers
    )

    expect(FakeWebSocket.instances).toHaveLength(1)
    expect(FakeWebSocket.last.url).toMatch(
      /\/api\/v1\/cameras\/cam-01\/streams\/main\/audio\/ws$/
    )
    // 不设 arraybuffer 的话，二进制帧会以 Blob 到达，20 字节头解析器永远等不到 ArrayBuffer。
    expect(FakeWebSocket.last.binaryType).toBe('arraybuffer')
    expect(channel.state).toBe('connecting')

    channel.close()
  })

  it('audio_info → onInfo，并把状态推进到 open', () => {
    const handlers = handlersStub()
    const channel = connectAudioChannel(
      { cameraId: 'cam-01', role: 'sub' },
      handlers
    )
    expect(FakeWebSocket.last.url).toMatch(/\/streams\/sub\/audio\/ws$/)

    FakeWebSocket.last.emitText(
      JSON.stringify({
        type: 'audio_info',
        hasAudio: true,
        codec: 'pcma',
        sampleRate: 8000,
        channels: 1,
        config: '',
      })
    )

    expect(handlers.onInfo).toHaveBeenCalledTimes(1)
    expect(handlers.onInfo).toHaveBeenCalledWith({
      codec: 'pcma',
      sampleRate: 8000,
      channels: 1,
      config: '',
    })
    expect(channel.state).toBe('open')

    channel.close()
  })

  it('AAC 的 audio_info 原样透出 base64 的 AudioSpecificConfig', () => {
    const handlers = handlersStub()
    const channel = connectAudioChannel(
      { cameraId: 'cam-01', role: 'main' },
      handlers
    )

    FakeWebSocket.last.emitText(
      JSON.stringify({
        type: 'audio_info',
        hasAudio: true,
        codec: 'aac',
        sampleRate: 48000,
        channels: 2,
        config: 'GjBA',
      })
    )

    expect(handlers.onInfo).toHaveBeenCalledWith({
      codec: 'aac',
      sampleRate: 48000,
      channels: 2,
      config: 'GjBA',
    })

    channel.close()
  })

  it('20 字节头解析：int64 的 pts_us 不截断到 32 位，payload 逐字节一致', () => {
    const handlers = handlersStub()
    const channel = connectAudioChannel(
      { cameraId: 'cam-01', role: 'main' },
      handlers
    )

    const payload = new Uint8Array([0x01, 0x02, 0xfe, 0xff])
    // 超过 2^32 的负数：用 getUint32 只取低位会解出 +695516296 之类的垃圾值。
    const largeNegativePts = -9_000_000_123
    FakeWebSocket.last.emitBinary(
      buildAudioFrame({
        flags: 0x01,
        seq: 4_294_967_290,
        ptsUs: largeNegativePts,
        payload,
      })
    )

    expect(handlers.onFrame).toHaveBeenCalledTimes(1)
    const frame = handlers.onFrame.mock.calls[0][0] as AudioFrame
    expect(frame.ptsUs).toBe(largeNegativePts)
    expect(frame.codec).toBe(AUDIO_CODEC_PCMA)
    expect(frame.flags).toBe(0x01)
    expect(frame.seq).toBe(4_294_967_290)
    expect(Array.from(frame.payload)).toEqual(Array.from(payload))

    channel.close()
  })

  it('20 字节头解析：超过 2^32 的正数同样精确', () => {
    const handlers = handlersStub()
    const channel = connectAudioChannel(
      { cameraId: 'cam-01', role: 'main' },
      handlers
    )

    const largePts = 4_294_967_296 + 12_345
    FakeWebSocket.last.emitBinary(buildAudioFrame({ ptsUs: largePts }))

    const frame = handlers.onFrame.mock.calls[0][0] as AudioFrame
    expect(frame.ptsUs).toBe(largePts)

    channel.close()
  })

  it('magic 不符 / 长度不足 → 丢帧且不抛，后续帧仍能处理', () => {
    const handlers = handlersStub()
    const channel = connectAudioChannel(
      { cameraId: 'cam-01', role: 'main' },
      handlers
    )

    expect(() => {
      FakeWebSocket.last.emitBinary(buildAudioFrame({ magic: 0x5a4c4d32 }))
    }).not.toThrow()
    expect(() => {
      // 19 字节：刚好比头短一字节。
      FakeWebSocket.last.emitBinary(new ArrayBuffer(AUDIO_WIRE_HEADER_SIZE - 1))
    }).not.toThrow()

    expect(handlers.onFrame).not.toHaveBeenCalled()
    expect(channel.droppedFrames).toBe(2)

    // 坏帧不得把这条连接弄哑：紧随其后的好帧照常处理。
    FakeWebSocket.last.emitBinary(buildAudioFrame({ seq: 9 }))
    expect(handlers.onFrame).toHaveBeenCalledTimes(1)
    expect((handlers.onFrame.mock.calls[0][0] as AudioFrame).seq).toBe(9)
    expect(channel.droppedFrames).toBe(2)

    channel.close()
  })

  it('未知文本帧静默忽略，不影响状态也不触发任何回调', () => {
    const handlers = handlersStub()
    const channel = connectAudioChannel(
      { cameraId: 'cam-01', role: 'main' },
      handlers
    )

    FakeWebSocket.last.emitText('not json at all')
    FakeWebSocket.last.emitText(JSON.stringify({ type: 'something_else' }))

    expect(handlers.onInfo).not.toHaveBeenCalled()
    expect(handlers.onFatal).not.toHaveBeenCalled()
    expect(channel.state).toBe('connecting')

    channel.close()
  })

  it('error 文本帧 → onFatal 且不再重连', () => {
    const handlers = handlersStub()
    const channel = connectAudioChannel(
      { cameraId: 'cam-01', role: 'main' },
      handlers
    )
    const socket = FakeWebSocket.last

    socket.emitText(
      JSON.stringify({
        type: 'error',
        code: 'NO_AUDIO',
        reason: 'camera reports no playable audio track',
      })
    )

    expect(handlers.onFatal).toHaveBeenCalledTimes(1)
    expect(handlers.onFatal).toHaveBeenCalledWith(
      'NO_AUDIO',
      'camera reports no playable audio track'
    )
    // 终止性帧要顺手把连接关掉：留着一条不再送数据的 socket 只会吃掉后端的通道配额。
    expect(socket.closeCalls).toBe(1)
    expect(channel.state).toBe('closed')

    vi.advanceTimersByTime(5000)
    expect(FakeWebSocket.instances).toHaveLength(1)
    expect(handlers.onDisconnect).not.toHaveBeenCalled()
  })

  it('关闭码 4001（被顶替）→ onDisconnect，退避后重连', () => {
    const handlers = handlersStub()
    const channel = connectAudioChannel(
      { cameraId: 'cam-01', role: 'main' },
      handlers
    )

    FakeWebSocket.last.emitClose(4001)

    expect(handlers.onDisconnect).toHaveBeenCalledWith(4001)
    expect(channel.state).toBe('closed')
    expect(FakeWebSocket.instances).toHaveLength(1)

    // 首帧退避 100ms（含抖动后 ≤ 100ms）。
    vi.advanceTimersByTime(100)
    expect(FakeWebSocket.instances).toHaveLength(2)
    expect(channel.state).toBe('connecting')

    channel.close()
  })

  it('关闭码 4404（该路无音频）→ onFatal("NO_AUDIO") 且不重连', () => {
    const handlers = handlersStub()
    const channel = connectAudioChannel(
      { cameraId: 'cam-01', role: 'main' },
      handlers
    )

    FakeWebSocket.last.emitClose(4404)

    expect(handlers.onFatal).toHaveBeenCalledWith(
      'NO_AUDIO',
      expect.stringContaining('no playable audio track')
    )
    expect(channel.state).toBe('closed')

    vi.advanceTimersByTime(5000)
    expect(FakeWebSocket.instances).toHaveLength(1)
  })

  it('网络断开（1006）→ 静默退避重连，不报 onFatal', () => {
    const handlers = handlersStub()
    const channel = connectAudioChannel(
      { cameraId: 'cam-01', role: 'main' },
      handlers
    )

    FakeWebSocket.last.emitClose(1006)
    expect(handlers.onFatal).not.toHaveBeenCalled()

    vi.advanceTimersByTime(100)
    expect(FakeWebSocket.instances).toHaveLength(2)

    channel.close()
  })

  it('close() 之后推进 5s，不再有任何新连接', () => {
    const handlers = handlersStub()
    const channel = connectAudioChannel(
      { cameraId: 'cam-01', role: 'main' },
      handlers
    )
    const socket = FakeWebSocket.last

    channel.close()
    expect(socket.closeCalls).toBe(1)
    expect(channel.state).toBe('closed')

    // 即使旧 socket 的 onclose 在 close() 之后才到，也必须不重连。
    socket.emitClose(1006)

    vi.advanceTimersByTime(5000)
    expect(FakeWebSocket.instances).toHaveLength(1)
    expect(handlers.onDisconnect).not.toHaveBeenCalled()
  })

  it('close() 能取消已经排上的重连定时器', () => {
    const handlers = handlersStub()
    const channel = connectAudioChannel(
      { cameraId: 'cam-01', role: 'main' },
      handlers
    )

    // 先断开以便排上重连，但立刻 close() —— 定时器必须被清掉。
    FakeWebSocket.last.emitClose(4003)
    channel.close()

    vi.advanceTimersByTime(5000)
    expect(FakeWebSocket.instances).toHaveLength(1)
  })

  it('多次重连按 100 → 200 → 400 退避（每级都带抖动，不超过标称值）', () => {
    const handlers = handlersStub()
    const channel = connectAudioChannel(
      { cameraId: 'cam-01', role: 'main' },
      handlers
    )

    // 每次推进该级退避的**标称值**：抖动只会让实际延迟更短，因此这是上界。
    // 第 1 次：base 100 → 抖动后 ∈ [50, 100]
    FakeWebSocket.last.emitClose(1006)
    vi.advanceTimersByTime(100)
    expect(FakeWebSocket.instances).toHaveLength(2)

    // 第 2 次：base 200 → 抖动后 ∈ [100, 200]
    FakeWebSocket.last.emitClose(1006)
    vi.advanceTimersByTime(200)
    expect(FakeWebSocket.instances).toHaveLength(3)

    // 第 3 次：base 400 → 抖动后 ∈ [200, 400]
    FakeWebSocket.last.emitClose(1006)
    vi.advanceTimersByTime(400)
    expect(FakeWebSocket.instances).toHaveLength(4)

    // 第 4 次：base 800 → 抖动后 ∈ [400, 800]
    FakeWebSocket.last.emitClose(1006)
    vi.advanceTimersByTime(800)
    expect(FakeWebSocket.instances).toHaveLength(5)

    // 第 5 次：base 本应 1600，封顶 1000 → 抖动后 ∈ [500, 1000]
    FakeWebSocket.last.emitClose(1006)
    vi.advanceTimersByTime(1000)
    expect(FakeWebSocket.instances).toHaveLength(6)

    channel.close()
  })
})
