import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { LivePlayer } from './live-player'

/**
 * `LivePlayer` 的音频接线契约（父任务 AC-4 的自动化部分）。
 *
 * 三条硬约束，全部在这里钉住：
 *   1. 不点喇叭（`isMuted = true`）→ **不建立任何音频连接**；`hasAudio = false` → 同样不建；
 *   2. 解禁 → 建 player + 建通道，且 `onFrame` 的帧真的进了帧解码器；
 *   3. 静音 / 卸载 → 通道 `close()` 与 player `close()` 都发生过（无残留连接与 AudioContext）。
 *
 * 音频播放器与 WS 通道都用替身：真实 `AudioContext` 在无手势的浏览器里是 `suspended`，
 * 单测里既听不到也验不了；而真实 WS 会去连后端。这里要验的是**组件的生命周期**，
 * 不是播放器/通道的内部实现（那两块各有自己的单测）。
 */

const mocks = vi.hoisted(() => {
  const state = {
    playerStarts: 0,
    playerCloses: 0,
    playerFlush: 0,
    volumes: [] as number[],
    degraded: false,
    connectCalls: [] as Array<{ cameraId: string; role: string }>,
    channelCloses: 0,
    /** 最新一次 connect 注册的 handler，测试用它推帧。 */
    handlers: null as null | {
      onInfo: (info: unknown) => void
      onFrame: (frame: unknown) => void
      onFatal: (code: string, reason?: string) => void
    },
    decoderPushCalls: [] as unknown[],
    decoderCloses: 0,
    decodersCreated: 0,
    decoderDegraded: false,
  }
  return state
})

vi.mock('../services/live-audio-player', () => ({
  createLiveAudioPlayer: (options: { volume?: number }) => {
    mocks.volumes.push(options.volume ?? -1)
    return {
      start: async () => {
        mocks.playerStarts++
      },
      push: vi.fn(),
      flush: () => {
        mocks.playerFlush++
      },
      setMuted: vi.fn(),
      setVolume: (volume: number) => {
        mocks.volumes.push(volume)
      },
      close: async () => {
        mocks.playerCloses++
      },
      get stats() {
        return { degraded: mocks.degraded }
      },
    }
  },
}))

vi.mock('../services/audio-channel', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('../services/audio-channel')>()
  return {
    ...actual,
    connectAudioChannel: (
      params: { cameraId: string; role: string },
      handlers: NonNullable<typeof mocks.handlers>
    ) => {
      mocks.connectCalls.push({ cameraId: params.cameraId, role: params.role })
      mocks.handlers = handlers
      return {
        close: () => {
          mocks.channelCloses++
        },
        state: 'connecting' as const,
        droppedFrames: 0,
      }
    },
  }
})

vi.mock('../services/audio-frame-decoder', () => ({
  createAudioFrameDecoder: async () => {
    mocks.decodersCreated++
    return {
      get degraded() {
        return mocks.decoderDegraded
      },
      reason: null,
      stats: { decoded: 0, dropped: 0 },
      push: (frame: unknown) => {
        mocks.decoderPushCalls.push(frame)
      },
      close: () => {
        mocks.decoderCloses++
      },
    }
  },
}))

/** 等到某个断言成立（音频建连链路上有多个 await）。 */
async function waitFor(
  predicate: () => boolean,
  timeoutMs = 2000
): Promise<void> {
  const deadline = performance.now() + timeoutMs
  while (performance.now() < deadline) {
    if (predicate()) return
    await new Promise((resolve) => setTimeout(resolve, 10))
  }
  throw new Error('waitFor timeout')
}

describe('LivePlayer 音频接线', () => {
  beforeEach(() => {
    mocks.playerStarts = 0
    mocks.playerCloses = 0
    mocks.playerFlush = 0
    mocks.volumes = []
    mocks.degraded = false
    mocks.connectCalls = []
    mocks.channelCloses = 0
    mocks.handlers = null
    mocks.decoderPushCalls = []
    mocks.decoderCloses = 0
    mocks.decodersCreated = 0
    mocks.decoderDegraded = false
  })

  it('默认静音：不建立任何音频连接', async () => {
    const { unmount } = await render(<LivePlayer cameraId='cam-quiet' />)
    // 给足时间让可能存在的 effect 跑起来
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(mocks.connectCalls).toHaveLength(0)
    expect(mocks.playerStarts).toBe(0)
    unmount()
  })

  it('hasAudio=false：即使解禁也不建立任何音频连接', async () => {
    const { unmount } = await render(
      <LivePlayer cameraId='cam-mute' hasAudio={false} isMuted={false} />
    )
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(mocks.connectCalls).toHaveLength(0)
    expect(mocks.playerStarts).toBe(0)
    unmount()
  })

  it('解禁：建 player 与音频通道，帧进解码器', async () => {
    const { unmount } = await render(
      <LivePlayer cameraId='cam-loud' isMuted={false} streamType='sub' />
    )

    await waitFor(() => mocks.connectCalls.length === 1)
    expect(mocks.playerStarts).toBe(1)
    expect(mocks.connectCalls[0]).toEqual({ cameraId: 'cam-loud', role: 'sub' })

    // 模拟后端下发能力帧 + 一帧音频
    mocks.handlers?.onInfo({
      codec: 'pcma',
      sampleRate: 8000,
      channels: 1,
      config: '',
    })
    // 解码器工厂是 async 的：必须等它 resolve，帧才会被分发（否则落在暂存队列里）
    await waitFor(() => mocks.decodersCreated === 1)
    mocks.handlers?.onFrame({
      codec: 0x11,
      flags: 0,
      seq: 1,
      ptsUs: 1000,
      payload: new Uint8Array(4),
    })
    await waitFor(() => mocks.decoderPushCalls.length === 1)
    unmount()
  })

  it('从解禁切回静音：通道与 player 都被关闭', async () => {
    const { rerender } = await render(
      <LivePlayer cameraId='cam-toggle' isMuted={false} />
    )
    await waitFor(() => mocks.connectCalls.length === 1)

    rerender(<LivePlayer cameraId='cam-toggle' isMuted={true} />)
    await waitFor(() => mocks.channelCloses === 1)
    expect(mocks.playerCloses).toBe(1)
  })

  it('卸载：通道与 player 都被关闭', async () => {
    const { unmount } = await render(
      <LivePlayer cameraId='cam-unmount' isMuted={false} />
    )
    await waitFor(() => mocks.connectCalls.length === 1)
    unmount()
    await waitFor(() => mocks.channelCloses === 1)
    expect(mocks.playerCloses).toBe(1)
  })

  it('AudioWorklet 降级时不建 WS：没有意义去接一段放不了的声音', async () => {
    mocks.degraded = true
    const { unmount } = await render(
      <LivePlayer cameraId='cam-degraded' isMuted={false} />
    )
    await waitFor(() => mocks.playerStarts === 1)
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(mocks.connectCalls).toHaveLength(0)
    unmount()
  })
})
