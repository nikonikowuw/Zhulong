import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { StreamConnectionPool } from './stream-pool'

describe('StreamConnectionPool', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('单例模式正确返回同一实例', () => {
    const pool1 = StreamConnectionPool.getInstance()
    const pool2 = StreamConnectionPool.getInstance()
    expect(pool1).toBe(pool2)
  })

  it('多个订阅者订阅同一摄像机时复用连接并正确维护引用计数', () => {
    const pool = StreamConnectionPool.getInstance()
    const cameraId = 'test-cam-01'

    const sub1Status = vi.fn()
    const sub1Stats = vi.fn()
    const sub2Status = vi.fn()
    const sub2Stats = vi.fn()

    // 订阅 1
    const unsub1 = pool.subscribe(cameraId, 'main', {
      id: 'sub-1',
      onStatusChange: sub1Status,
      onStatsUpdate: sub1Stats,
    })

    expect(pool.getStreamRefCount(cameraId, 'main')).toBe(1)
    expect(sub1Status).toHaveBeenCalledWith('idle', undefined)

    // 订阅 2 (复用)
    const unsub2 = pool.subscribe(cameraId, 'main', {
      id: 'sub-2',
      onStatusChange: sub2Status,
      onStatsUpdate: sub2Stats,
    })

    expect(pool.getStreamRefCount(cameraId, 'main')).toBe(2)

    // 更新统计，两位订阅者均能收到广播
    pool.updateStats(cameraId, 'main', { fps: 30 })
    expect(sub1Stats).toHaveBeenCalledWith(expect.objectContaining({ fps: 30 }))
    expect(sub2Stats).toHaveBeenCalledWith(expect.objectContaining({ fps: 30 }))

    // 订阅 1 退出，计数减为 1，不销毁连接
    unsub1()
    expect(pool.getStreamRefCount(cameraId, 'main')).toBe(1)

    // 订阅 2 退出，计数减为 0
    unsub2()
    expect(pool.getStreamRefCount(cameraId, 'main')).toBe(0)

    // 快进 3 秒 (Grace Period 3s 防抖)
    vi.advanceTimersByTime(3000)
    expect(pool.getStreamRefCount(cameraId, 'main')).toBe(0)
  })
})

/**
 * 预览 WS 的文本能力帧（`stream_info`）。
 *
 * 铁律：`ArrayBuffer` 分支一行不改，fps/码率统计只算视频。这批用例的全部目的
 * 就是把这条铁律钉成可执行的断言 —— 文本帧一旦掉进二进制分支，既会把 JSON
 * 字节算进码率，又会把 JSON 当成「原始视频负载」广播给渲染层。
 */
describe('StreamConnectionPool · 文本能力帧', () => {
  /** 可控的假 WebSocket：用来把确定性的帧灌进池子，不依赖真实网络。 */
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

    binaryType = 'blob'
    readyState = 0
    onopen: (() => void) | null = null
    onerror: (() => void) | null = null
    onmessage: ((event: MessageEvent) => void) | null = null
    onclose: ((event: CloseEvent) => void) | null = null

    constructor(readonly url: string) {
      FakeWebSocket.instances.push(this)
    }

    close(): void {
      this.readyState = 3
    }

    emitText(data: string): void {
      this.onmessage?.({ data } as MessageEvent)
    }

    emitBinary(data: ArrayBuffer): void {
      this.onmessage?.({ data } as MessageEvent)
    }
  }

  /** 一帧合法的 ZLM2 视频包（载荷固定，便于按字节数反推码率）。 */
  function buildZlm2Packet(seq: number): ArrayBuffer {
    // 1000 字节载荷：让「每帧字节数」与「每行 JSON 字符数」在量级上可区分。
    const payload = new Uint8Array(1000).fill(0x88)
    payload.set([0, 0, 0, 1, 0x67, 0x42, 0xe0, 0x1e], 0)
    const headerSize = 32
    const buffer = new ArrayBuffer(headerSize + payload.length)
    const view = new DataView(buffer)
    view.setUint32(0, 0x5a4c4d32, false) // "ZLM2"
    view.setUint8(4, 2) // protocol version
    view.setUint8(5, 0x01) // H.264
    view.setUint8(6, 0x01) // IDR
    view.setUint8(7, 0x01) // keyframe
    view.setBigInt64(8, 1_000_000n, false)
    view.setBigInt64(16, 1_000_000n, false)
    view.setUint32(24, seq, false)
    view.setUint32(28, 0, false)
    new Uint8Array(buffer).set(payload, headerSize)
    return buffer
  }

  function buildStreamInfo(cameraId: string, v: number): string {
    return JSON.stringify({
      type: 'stream_info',
      cameraId,
      role: 'main',
      v,
      audio: {
        present: true,
        codec: 'pcma',
        sampleRate: 8000,
        channels: 1,
        config: '',
      },
    })
  }

  beforeEach(() => {
    // 统计窗口按墙钟切片（`Date.now()`），必须同时冻结计时器与 Date 才能确定。
    vi.useFakeTimers()
    FakeWebSocket.reset()
    vi.stubGlobal('WebSocket', FakeWebSocket)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('文本帧不进统计、不进 onFrame / onPacket；二进制帧照旧各计 5 次', () => {
    const pool = StreamConnectionPool.getInstance()
    const cameraId = 'pool-text-frame-stats'
    const onStatsUpdate = vi.fn()
    const onFrame = vi.fn()
    const onPacket = vi.fn()
    const onStreamInfo = vi.fn()

    const unsubscribe = pool.subscribe(cameraId, 'main', {
      id: 'text-frame-sub',
      onStatusChange: () => {},
      onStatsUpdate,
      onFrame,
      onPacket,
      onStreamInfo,
    })

    const socket = FakeWebSocket.last
    expect(socket.binaryType).toBe('arraybuffer')

    // 先进 10 条文本帧，再推进满一个 1s 统计窗口。
    // 若文本帧误入二进制分支，下面的 fps 会是 11（10 条文 + 1 帧视频）而不是 1。
    for (let i = 1; i <= 10; i++) {
      socket.emitText(buildStreamInfo(cameraId, i))
    }
    vi.advanceTimersByTime(1000)

    const packets = Array.from({ length: 5 }, (_, i) => buildZlm2Packet(i + 1))
    for (const packet of packets) socket.emitBinary(packet)

    expect(onStreamInfo).toHaveBeenCalledTimes(10)
    expect(onStreamInfo).toHaveBeenLastCalledWith(
      expect.objectContaining({ type: 'stream_info', v: 10 })
    )

    expect(onPacket).toHaveBeenCalledTimes(5)
    expect(onFrame).toHaveBeenCalledTimes(5)

    // 首个视频帧恰好踢满 1.0s 统计窗口 → fps=1、码率按「一帧视频」的字节数算。
    const oneVideoFrameBytes = packets[0].byteLength
    expect(onStatsUpdate).toHaveBeenLastCalledWith(
      expect.objectContaining({
        fps: 1,
        bitrateKbps: Math.round((oneVideoFrameBytes * 8) / 1 / 1000),
      })
    )

    unsubscribe()
  })

  it('后挂的订阅者能拿到缓存的 stream_info', () => {
    const pool = StreamConnectionPool.getInstance()
    const cameraId = 'pool-text-frame-replay'

    const unsubscribeFirst = pool.subscribe(cameraId, 'main', {
      id: 'replay-sub-1',
      onStatusChange: () => {},
      onStatsUpdate: () => {},
    })

    FakeWebSocket.last.emitText(buildStreamInfo(cameraId, 7))

    const lateInfo = vi.fn()
    const unsubscribeLate = pool.subscribe(cameraId, 'main', {
      id: 'replay-sub-2',
      onStatusChange: () => {},
      onStatsUpdate: () => {},
      onStreamInfo: lateInfo,
    })

    // 「先连上、后挂订阅者」：能力帧只在建连时下发一次，不补发就永远等不到。
    expect(lateInfo).toHaveBeenCalledTimes(1)
    expect(lateInfo).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'stream_info',
        cameraId,
        role: 'main',
        v: 7,
        audio: expect.objectContaining({ present: true, codec: 'pcma' }),
      })
    )

    unsubscribeFirst()
    unsubscribeLate()
  })

  it('非 JSON / 非 stream_info 文本帧被静默忽略，不动任何状态', () => {
    const pool = StreamConnectionPool.getInstance()
    const cameraId = 'pool-text-frame-invalid'
    const onStreamInfo = vi.fn()
    const onFrame = vi.fn()
    const onPacket = vi.fn()
    const onStatusChange = vi.fn()

    const unsubscribe = pool.subscribe(cameraId, 'main', {
      id: 'invalid-sub',
      onStatusChange,
      onStatsUpdate: () => {},
      onFrame,
      onPacket,
      onStreamInfo,
    })
    onStatusChange.mockClear()

    const socket = FakeWebSocket.last
    expect(() => {
      socket.emitText('not json at all')
      socket.emitText(JSON.stringify({ type: 'something_else', v: 1 }))
      socket.emitText(JSON.stringify({ type: 'stream_info' })) // 缺 audio
    }).not.toThrow()

    expect(onStreamInfo).not.toHaveBeenCalled()
    expect(onFrame).not.toHaveBeenCalled()
    expect(onPacket).not.toHaveBeenCalled()
    expect(onStatusChange).not.toHaveBeenCalled()

    unsubscribe()
  })
})
