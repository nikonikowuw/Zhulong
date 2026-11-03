import { useEffect, useRef } from 'react'
import {
  connectAudioChannel,
  type AudioChannel,
  type AudioFrame,
} from '../services/audio-channel'
import {
  createAudioFrameDecoder,
  type AudioFrameDecoder,
} from '../services/audio-frame-decoder'
import {
  createLiveAudioPlayer,
  type LiveAudioPlayer,
  type PresentationClockSnapshot,
} from '../services/live-audio-player'
import { type StreamType } from '../types'

/** 喇叭按钮/播放器侧的音频状态。 */
export type LivePlayerAudioStatus =
  | 'idle'
  | 'connecting'
  | 'active'
  | 'no_audio'
  | 'unsupported'

/**
 * 解码器就绪前允许缓存的帧数上限。
 *
 * AAC 的 `isConfigSupported()` 是异步的，那几十毫秒里到的 AU 不该丢（丢了下一次声音
 * 从半句开始）；但无限缓存在「收到了帧却永远等不到 info」的情形下会无界增长，
 * 50 帧 ≈ 1 秒的最新音频，足够覆盖这个窗口。
 */
const MAX_PENDING_AUDIO_FRAMES = 50

export interface UseLiveAudioOptions {
  cameraId: string | null | undefined
  streamType: StreamType
  /** `false` = 该路没有可播的音频：不建任何 WS / AudioContext。 */
  hasAudio: boolean
  /** `true` = 用户静音（默认）。只有 `false` 时才建连接。 */
  muted: boolean
  volume: number
  /** 视频呈现时钟快照（音画同一条时间轴）。 */
  getClock: () => PresentationClockSnapshot | null
  onStatusChange?: (status: LivePlayerAudioStatus) => void
}

export interface UseLiveAudioReturn {
  /**
   * 丢弃已排队的音频（视频 `onResync` 时调用）。
   *
   * 走 ref 读播放器：`onResync` 是每秒可能触发多次的路径，不能让它依赖任何 state。
   */
  flush: () => void
}

/**
 * 音频通道的完整生命周期：建 WS → 解 ASC → 解码 → 投递 PCM。
 *
 * ## 为什么单独一个 hook
 *
 * 这段逻辑有三个必须跨 render 存活的对象（`LiveAudioPlayer` / `AudioChannel` /
 * `AudioFrameDecoder`），全部塞进 `LivePlayer` 会让组件同时承担渲染与音频编排；
 * 更实际的问题是任何一次重渲染都可能重建它们，表现为「每次交互声音断一下」。
 *
 * ## 降级契约（design.md 降级矩阵）
 *
 * 任何一环不可用都只把状态置成 `no_audio` / `unsupported` 并**静默退出** ——
 * 视频必须继续。
 */
export function useLiveAudio({
  cameraId,
  streamType,
  hasAudio,
  muted,
  volume,
  getClock,
  onStatusChange,
}: UseLiveAudioOptions): UseLiveAudioReturn {
  /** 播放器实例走 ref：`flush()` 要在 effect 之外被调用。 */
  const playerRef = useRef<LiveAudioPlayer | null>(null)

  /**
   * 回调与音量的最新值。
   *
   * ⚠️ 不能在 render 里写 `.current`（React 19 下 eslint 会直接报
   * `Cannot access refs during render`）：那相当于在渲染期间产生副作用，
   * 并发渲染下 ref 可能被写入到一个不会提交的渲染里。这两个都改用 effect 同步。
   *
   * 它们的唯一用途是「让 effect 内的闭包读到当前值而不进依赖数组」，
   * 因此先后一个 tick 并不会造成行为差异：真正需要它们的路径（建连接 / 改音量）
   * 都在 effect 之后才发生。
   */
  const onStatusChangeRef = useRef(onStatusChange)
  useEffect(() => {
    onStatusChangeRef.current = onStatusChange
  }, [onStatusChange])

  /** 音量在 effect 内读取当前值，不进依赖数组（拖滑杆不该重启连接）。 */
  const volumeRef = useRef(volume)
  useEffect(() => {
    volumeRef.current = volume
  }, [volume])

  useEffect(() => {
    if (!hasAudio) {
      onStatusChangeRef.current?.('no_audio')
      return
    }
    if (!cameraId || muted) {
      onStatusChangeRef.current?.('idle')
      return
    }

    let disposed = false
    let decoder: AudioFrameDecoder | null = null
    /** 解码器就绪之前收到的帧（AAC 的 isConfigSupported 是异步的）。 */
    let pending: AudioFrame[] = []
    let channel: AudioChannel | null = null

    const player = createLiveAudioPlayer({
      getClock,
      volume: volumeRef.current,
    })
    playerRef.current = player
    onStatusChangeRef.current?.('connecting')

    const reportDegrade = (): void => {
      // 降级：禁用音频轨，视频继续。用户看到的只是喇叭弹回置灰 + 文案。
      onStatusChangeRef.current?.('unsupported')
    }

    const start = async (): Promise<void> => {
      // 先建图再连通道：反过来的话，`onInfo` 会赶在播放器就绪前到达，
      // 首包只能落到 100 ms 暂存区里，白白多一段静音。
      await player.start()
      if (disposed) return
      if (player.stats.degraded) {
        // AudioWorklet 不可用：不必再建一条 WS 去接根本放不了的声音。
        reportDegrade()
        return
      }

      channel = connectAudioChannel(
        { cameraId, role: streamType },
        {
          onInfo: (info) => {
            if (disposed) return
            void createAudioFrameDecoder({
              info,
              player,
              onDegrade: reportDegrade,
            }).then((created) => {
              if (disposed) {
                created.close()
                return
              }
              decoder = created
              if (!created.degraded) onStatusChangeRef.current?.('active')
              // 解码器就绪前攒下的帧按顺序补投（不含历史：只在就绪窗口内）。
              const queued = pending
              pending = []
              for (const frame of queued) created.push(frame)
            })
          },
          onFrame: (frame) => {
            if (disposed) return
            if (decoder) {
              decoder.push(frame)
              return
            }
            if (pending.length >= MAX_PENDING_AUDIO_FRAMES) pending.shift()
            pending.push(frame)
          },
          onFatal: (code) => {
            if (disposed) return
            // NO_AUDIO = 相机真的没声音；其余（UNSUPPORTED / 内部错误）= 这路放不了。
            onStatusChangeRef.current?.(
              code === 'NO_AUDIO' ? 'no_audio' : 'unsupported'
            )
          },
        }
      )
    }

    void start()

    return () => {
      disposed = true
      pending = []
      // 顺序：先去帧源 → 再丢解码器 → 最后关上下文。
      // 反过来会让解码器在已关闭的播放器上 push 一帧（write after close）。
      channel?.close()
      decoder?.close()
      if (playerRef.current === player) playerRef.current = null
      void player.close()
    }
  }, [cameraId, streamType, muted, hasAudio, getClock])

  // 音量与「在哪一路出声」正交：单独一条 effect 实时下发，不重启连接。
  useEffect(() => {
    playerRef.current?.setVolume(volume)
  }, [volume])

  return {
    flush: () => playerRef.current?.flush(),
  }
}
