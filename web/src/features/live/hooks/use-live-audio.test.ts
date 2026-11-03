import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { renderHook } from 'vitest-browser-react'
import { useLiveAudio } from './use-live-audio'

/**
 * `useLiveAudio` 的契约。
 *
 * 组件层（`live-player-audio.test.tsx`）验的是「LivePlayer 会不会按 prop 建/拆音频」，
 * 这里验的是 hook 自己那几条**只有 hook 层才有的**约束：
 *
 *   1. 不满足 `!muted && hasAudio && cameraId` 时，**一个东西都不建**；
 *   2. AAC 的 `createAudioFrameDecoder` 是异步的，就绪前到的帧不能丢，且就绪后**按序**补投；
 *   3. 卸载顺序必须是「通道 → 解码器 → 播放器」：反过来会让解码器往已关闭的播放器里塞帧；
 *   4. `getClock` / `volume` 变化不得重建连接（否则拖音量滑杆 = 每秒重连一次）。
 */

const mocks = vi.hoisted(() => ({
  players: [] as Array<{
    closes: number
    volumes: number[]
    pushes: unknown[]
    flushes: number
  }>,
  channels: [] as Array<{
    closes: number
    handlers: Record<string, unknown>
    params: { cameraId: string; role: string }
  }>,
  decoders: [] as Array<{ pushes: unknown[]; closes: number }>,
  decoderFactoryGate: null as null | ((release: () => void) => void),
  /** 非空时，三个 close 各自把名字追加进来，用于断言卸载顺序。 */
  onCloseOrder: null as null | string[],
}))

vi.mock('../services/live-audio-player', () => ({
  createLiveAudioPlayer: (options: { volume?: number }) => {
    const player = {
      closes: 0,
      volumes: [] as number[],
      pushes: [] as unknown[],
      flushes: 0,
    }
    // 记录构造时收到的音量（hook 必须在 start 前把它交给播放器）。
    if (typeof options.volume === 'number') player.volumes.push(options.volume)
    const instance = {
      start: async () => undefined,
      push: (frame: unknown) => {
        player.pushes.push(frame)
      },
      flush: () => {
        player.flushes++
      },
      setMuted: vi.fn(),
      setVolume: (volume: number) => {
        player.volumes.push(volume)
      },
      close: async () => {
        player.closes++
        mocks.onCloseOrder?.push('player')
      },
      get stats() {
        // 本组用例不需要降级路径（那条在 live-player-audio.test.tsx 里验）。
        return { degraded: false }
      },
    }
    mocks.players.push(player)
    return instance
  },
}))

vi.mock('../services/audio-channel', () => ({
  connectAudioChannel: (
    params: { cameraId: string; role: string },
    handlers: Record<string, unknown>
  ) => {
    const channel = { closes: 0, handlers, params }
    mocks.channels.push(channel)
    return {
      close: () => {
        channel.closes++
        mocks.onCloseOrder?.push('channel')
      },
      get state() {
        return 'open'
      },
      get droppedFrames() {
        return 0
      },
    }
  },
}))

vi.mock('../services/audio-frame-decoder', () => ({
  createAudioFrameDecoder: async (options: { onDegrade: () => void }) => {
    const decoder = { pushes: [] as unknown[], closes: 0 }
    mocks.decoders.push(decoder)
    if (mocks.decoderFactoryGate) {
      await new Promise<void>((resolve) => mocks.decoderFactoryGate?.(resolve))
    }
    void options
    return {
      push: (frame: unknown) => {
        decoder.pushes.push(frame)
      },
      close: () => {
        decoder.closes++
        mocks.onCloseOrder?.push('decoder')
      },
      get degraded() {
        return false
      },
    }
  },
}))

function installCleanState(): void {
  mocks.players.length = 0
  mocks.channels.length = 0
  mocks.decoders.length = 0
  mocks.decoderFactoryGate = null
}

const BASE_OPTIONS = {
  cameraId: 'cam-1',
  streamType: 'main' as const,
  hasAudio: true,
  muted: false,
  volume: 0.5,
  getClock: () => ({ wallMs: 0, ptsUS: 0, targetLatencyMs: 100 }),
}

function makeFrame(ptsUs: number) {
  return { codec: 0x11, flags: 0, seq: 0, ptsUs, payload: new Uint8Array(160) }
}

describe('useLiveAudio', () => {
  beforeEach(installCleanState)
  afterEach(() => {
    vi.clearAllMocks()
  })

  it('静音时什么都不建（不建 WS、不建 AudioContext）', async () => {
    await renderHook(() => useLiveAudio({ ...BASE_OPTIONS, muted: true }))
    expect(mocks.players).toHaveLength(0)
    expect(mocks.channels).toHaveLength(0)
  })

  it('hasAudio=false 时什么都不建', async () => {
    await renderHook(() => useLiveAudio({ ...BASE_OPTIONS, hasAudio: false }))
    expect(mocks.players).toHaveLength(0)
    expect(mocks.channels).toHaveLength(0)
  })

  it('无 cameraId 时什么都不建', async () => {
    await renderHook(() => useLiveAudio({ ...BASE_OPTIONS, cameraId: null }))
    expect(mocks.players).toHaveLength(0)
    expect(mocks.channels).toHaveLength(0)
  })

  it('解禁 → 建一个播放器与一条通道', async () => {
    await renderHook(() => useLiveAudio(BASE_OPTIONS))
    await vi.waitFor(() => expect(mocks.channels).toHaveLength(1))
    expect(mocks.players).toHaveLength(1)
  })

  it('解码器就绪前到的帧不丢，就绪后按序补投', async () => {
    // 卡住解码器工厂，模拟 AAC 的 isConfigSupported 异步窗口。
    let release: (() => void) | null = null
    mocks.decoderFactoryGate = (resolve) => {
      release = resolve
    }

    await renderHook(() => useLiveAudio(BASE_OPTIONS))
    await vi.waitFor(() => expect(mocks.channels).toHaveLength(1))

    const handlers = mocks.channels[0].handlers as {
      onInfo: (info: unknown) => void
      onFrame: (frame: unknown) => void
    }
    handlers.onInfo({ codec: 'aac', sampleRate: 8000, channels: 1, config: '' })

    // 就绪前塞三帧：必须按 ptsUs 顺序进入解码器。
    handlers.onFrame(makeFrame(10))
    handlers.onFrame(makeFrame(20))
    handlers.onFrame(makeFrame(30))
    expect(mocks.decoders[0].pushes).toHaveLength(0)

    // TS 会把 `release` 推成 null（赋值发生在回调里）；用类型断言取回可调用形式。
    ;(release as unknown as () => void)?.()
    await vi.waitFor(() => expect(mocks.decoders[0].pushes).toHaveLength(3))
    expect(
      mocks.decoders[0].pushes.map((f) => (f as { ptsUs: number }).ptsUs)
    ).toEqual([10, 20, 30])
  })

  it('卸载顺序：通道 → 解码器 → 播放器', async () => {
    // 真正记录先后：在三个 close 上挂同一个记录器，按调用时间入队。
    // （只断言「都 close 过」是伪断言 —— 顺序错了同样会通过。）
    const order: string[] = []
    mocks.decoderFactoryGate = null
    mocks.onCloseOrder = order

    const view = await renderHook(() => useLiveAudio(BASE_OPTIONS))
    await vi.waitFor(() => expect(mocks.channels).toHaveLength(1))
    const handlers = mocks.channels[0].handlers as {
      onInfo: (info: unknown) => void
    }
    handlers.onInfo({
      codec: 'pcma',
      sampleRate: 8000,
      channels: 1,
      config: '',
    })
    await vi.waitFor(() => expect(mocks.decoders).toHaveLength(1))

    order.length = 0
    await view.unmount()

    // 反过来的话解码器会往已关闭的播放器里塞帧（write after close）。
    expect(order).toEqual(['channel', 'decoder', 'player'])
  })

  it('拖音量滑杆不重建连接', async () => {
    const view = await renderHook(
      (props: { volume: number } | undefined) =>
        useLiveAudio({ ...BASE_OPTIONS, volume: props?.volume ?? 0.5 }),
      { initialProps: { volume: 0.5 } }
    )

    await vi.waitFor(() => expect(mocks.channels).toHaveLength(1))

    await view.rerender({ volume: 0.1 })
    await view.rerender({ volume: 0.9 })

    // 音量只下发到已存在的播放器，绝不开第二条通道。
    expect(mocks.channels).toHaveLength(1)
    expect(mocks.players[0].volumes).toContain(0.1)
    expect(mocks.players[0].volumes).toContain(0.9)
  })
})
