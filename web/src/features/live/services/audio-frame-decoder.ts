/**
 * 音频帧 → PCM 的分派器：把「一条音频 WS 上的裸帧」变成 `LiveAudioPlayer.push()` 要的三件套
 * （frame / pcm / inputRate）。
 *
 * ## 为什么单独抽一个模块，而不是写在组件里
 *
 * `AudioDecoder`、`configure()` 的配置对象、`EncodedAudioChunk` 的构造都是**有生命周期**的对象，
 * 必须跨 render 存活。放在 `live-player.tsx` 里，任何一次重渲染（AI 叠加层开关、stats 回流、
 * 静音状态变化…）都会把它们重建一次：解码器重建意味着丢掉已排队的 AU，表现为每次交互后
 * 声音断一下。抽成工厂后，组件只持有一个 `useRef`。
 *
 * ## 降级契约（design.md 降级矩阵）
 *
 * 本模块**从不抛异常**。任何「播不了」的情形都收敛成 `degraded = true` + `reason`，
 * 然后 `push()` 变成空操作 —— 视频必须继续。三种原因：
 *
 *   - `no_decoder`：环境里没有 `AudioDecoder` / `EncodedAudioChunk`（非安全上下文、
 *     旧浏览器、Firefox 的部分版本）；
 *   - `unsupported_config`：ASC 解析不出来（缺 `config` / 非法 base64 / 不支持的 AOT），
 *     或 `AudioDecoder.isConfigSupported()` 回 false；
 *   - `decoder_error`：解码过程中 `AudioDecoder` 的 error 回调被触发。
 */
import { parseAudioSpecificConfig, type AacConfig } from './aac-decoder'
import {
  AUDIO_CODEC_AAC,
  AUDIO_FLAG_DISCONTINUITY,
  type AudioChannelInfo,
  type AudioFrame,
} from './audio-channel'
import { decodeG711 } from './g711-decoder'
import { type LiveAudioPlayer } from './live-audio-player'

/**
 * AAC 一帧的样本数（1024）。AAC-LC / HE-AAC 的 AAC 帧恒为 1024 个样本，
 * `EncodedAudioChunk.duration` 只是提示值（真正时长由解码器从码流得出），
 * 但给对了能让 Chromium 的 jitter 估算少一次校正。
 */
const AAC_SAMPLES_PER_FRAME = 1024

/** G.711 的标称采样率：RTP profile 固定 8 kHz（`audio_info.sampleRate` 缺省时用它）。 */
const G711_DEFAULT_RATE = 8000

/** 降级原因（供 UI 区分「该路无音频」与「浏览器不支持该音频格式」）。 */
export type AudioDegradeReason =
  | 'no_decoder'
  | 'unsupported_config'
  | 'decoder_error'
  | 'unknown_codec'

export interface AudioFrameDecoderOptions {
  /** 该路音频的能力信息（来自 `audio_info`）。 */
  info: AudioChannelInfo
  /** 播放器（只用到 push / flush，单测可传最小桩）。 */
  player: Pick<LiveAudioPlayer, 'push' | 'flush'>
  /** 降级时回调一次；UI 用它把喇叭置灰并给出文案。 */
  onDegrade?: (reason: AudioDegradeReason) => void
}

export interface AudioFrameDecoderStats {
  /** 成功交给播放器的帧数（AAC 以解码输出计数，不是输入 AU 计数）。 */
  decoded: number
  /** 降级后丢弃的帧数。 */
  dropped: number
}

export interface AudioFrameDecoder {
  /** 是否已降级为「无声」。降级后 `push()` 是空操作。 */
  readonly degraded: boolean
  /** 降级原因；未降级时为 null。 */
  readonly reason: AudioDegradeReason | null
  readonly stats: AudioFrameDecoderStats
  push(frame: AudioFrame): void
  close(): void
}

/** 空的 payload 常量：AAC 解码输出没有对应的「输入负载」。 */
const EMPTY_PAYLOAD = new Uint8Array(0)

/** 环境里是否有 WebCodecs 音频解码能力（非安全上下文 / 旧浏览器会缺）。 */
function hasAudioDecoder(): boolean {
  return (
    typeof globalThis.AudioDecoder === 'function' &&
    typeof globalThis.EncodedAudioChunk === 'function'
  )
}

/**
 * 把 `AudioData` 拷成单声道 Float32。
 *
 * 用 `f32-planar` 逐平面拷：`f32`（交错）虽然一次拷完，但多声道时要自己按步长取平均，
 * 而 planar 下每个平面就是一段连续样本，混音逻辑一眼能看懂。
 * 声道数 > 1 时按「各平面求和 / 声道数」下混 —— 摄像头音轨最多双声道，
 * 不需要 ITU-R BS.775 那种带中心声道加权的下混矩阵。
 */
function extractMono(data: AudioData): Float32Array {
  const frames = data.numberOfFrames
  const channels = Math.max(1, data.numberOfChannels)
  const mono = new Float32Array(frames)

  if (channels === 1) {
    data.copyTo(mono, { planeIndex: 0, format: 'f32-planar' })
    return mono
  }

  const plane = new Float32Array(frames)
  for (let channel = 0; channel < channels; channel++) {
    data.copyTo(plane, { planeIndex: channel, format: 'f32-planar' })
    for (let i = 0; i < frames; i++) mono[i] += plane[i]
  }
  for (let i = 0; i < frames; i++) mono[i] /= channels
  return mono
}

/**
 * 建立一条「帧 → PCM」分派器。
 *
 * AAC 需要先问 `isConfigSupported()`（异步），所以工厂本身是 async：调用方 await 之后
 * 拿到的分派器一定已经 `configure()` 完毕。拿不到可用配置时**不**返回 null，
 * 而是返回一个 `degraded = true` 的空实现 —— 调用方的调用点因此不需要分支。
 */
export async function createAudioFrameDecoder(
  options: AudioFrameDecoderOptions
): Promise<AudioFrameDecoder> {
  const { info, player, onDegrade } = options

  let closed = false
  let degraded = false
  let reason: AudioDegradeReason | null = null
  let decoder: AudioDecoder | null = null

  const stats: AudioFrameDecoderStats = { decoded: 0, dropped: 0 }

  function degrade(next: AudioDegradeReason): void {
    if (degraded || closed) return
    degraded = true
    reason = next
    // 关掉解码器：留着它只会继续吃 AU 再触发 error 回调，变成报错循环。
    const current = decoder
    decoder = null
    try {
      if (current && current.state !== 'closed') current.close()
    } catch {
      // 已关闭：忽略。
    }
    onDegrade?.(next)
  }

  /** 建立 AAC 解码器；失败返回 false（已置降级）。 */
  async function configureAac(aac: AacConfig): Promise<boolean> {
    if (!hasAudioDecoder()) {
      degrade('no_decoder')
      return false
    }

    const config: AudioDecoderConfig = {
      codec: aac.codec,
      // ⚠️ 采样率 / 声道取 **ASC 解析值**，不取 `info.sampleRate`。
      //
      // 后端（native）已经保证 `audio_info.sampleRate` / `channels` 以 ASC 为准
      // （见 `.trellis/tasks/11-03-dual-track-native/progress.md` §3：`mpeg4-generic/90000`
      // 会让 `codecpar->sample_rate` 变成 90000）。两者本该一致；万一漂移，
      // 解码器只认 ASC 描述的那一套参数，用 `info` 的值 configure 得到的是
      // 一段音调错误的噪声而不是声音。这里**不**改写 `info` 对象本身
      // （它是从能力帧反序列化来的只读快照），只决定喂给解码器的值。
      sampleRate: aac.sampleRate,
      numberOfChannels: aac.channels,
      description: aac.description,
    }

    try {
      const support = await AudioDecoder.isConfigSupported(config)
      if (!support.supported) {
        degrade('unsupported_config')
        return false
      }
    } catch {
      // isConfigSupported 对畸形 config 会 reject（TypeError）：等价于不支持。
      degrade('unsupported_config')
      return false
    }

    if (closed || degraded) return false

    try {
      decoder = new AudioDecoder({
        output: (data: AudioData) => {
          if (closed) {
            data.close()
            return
          }
          // 先把需要的字段读出来，再关掉 —— close() 之后再读是 Illegal state。
          const timestamp = data.timestamp
          const rate = data.sampleRate || aac.sampleRate
          try {
            const pcm = extractMono(data)
            player.push(
              {
                codec: AUDIO_CODEC_AAC,
                // 解码输出无法携带输入帧的 flags（AudioData 只回显 timestamp），
                // 因此 DISCONTINUITY 在 push() 里以 player.flush() 的形式处理。
                flags: 0,
                seq: 0,
                ptsUs: timestamp,
                payload: EMPTY_PAYLOAD,
              },
              pcm,
              rate
            )
            stats.decoded++
          } catch {
            // 下混 / 投递失败：丢这一帧，不影响后续。
          } finally {
            // ⚠️ 必须关。不关的 AudioData 会占住解码器的输出缓冲，
            // 几秒内就能把硬件音频解码器的队列塞满（v2 起浏览器不再替调用方回收）。
            data.close()
          }
        },
        error: () => {
          degrade('decoder_error')
        },
      })
      decoder.configure(config)
      return true
    } catch {
      degrade('unsupported_config')
      return false
    }
  }

  // AAC 的 ASC 只解析一次：解码器配置与「输入采样率」必须来自**同一个**结果，
  // 否则两处各自 parse 会在某次改动后悄悄用上不同的采样率。
  const aac =
    info.codec === 'aac' ? parseAudioSpecificConfig(info.config) : null

  if (info.codec === 'aac') {
    if (!aac) {
      // ASC 缺失 / 非法 / 不支持的 AOT：直接降级，绝不退回 info 里的字段硬配。
      degrade('unsupported_config')
    } else {
      await configureAac(aac)
    }
  } else if (info.codec !== 'pcma' && info.codec !== 'pcmu') {
    degrade('unknown_codec')
  }

  const inputRate = aac
    ? aac.sampleRate
    : info.sampleRate > 0
      ? info.sampleRate
      : G711_DEFAULT_RATE

  return {
    get degraded() {
      return degraded
    },
    get reason() {
      return reason
    },
    stats,

    push(frame: AudioFrame): void {
      if (closed) return
      if (degraded) {
        stats.dropped++
        return
      }

      if (info.codec === 'pcma' || info.codec === 'pcmu') {
        const pcm = decodeG711(info.codec, frame.payload)
        // G.711 的 RTP 时钟固定 8 kHz；`audio_info.sampleRate` 有值时以它为准
        // （16 kHz PCMA 虽然罕见，但按 8 kHz 投递会变成两倍速的花栗鼠音）。
        player.push(frame, pcm, inputRate)
        stats.decoded++
        return
      }

      // AAC：源流时间轴跳变时先让播放器清空环形缓冲。
      // 解码输出拿不到输入帧的 flags（见 output 回调注释），所以重锚必须在这里做。
      if ((frame.flags & AUDIO_FLAG_DISCONTINUITY) !== 0) player.flush()

      const current = decoder
      if (!current || current.state !== 'configured') {
        stats.dropped++
        return
      }

      try {
        current.decode(
          new EncodedAudioChunk({
            type: 'key',
            timestamp: frame.ptsUs,
            duration: Math.round(
              (AAC_SAMPLES_PER_FRAME * 1_000_000) / inputRate
            ),
            data: frame.payload,
          })
        )
      } catch {
        // 单帧畸形（空负载 / 解码器已进入错误态）：丢这一帧，不升级为降级。
        stats.dropped++
      }
    },

    close(): void {
      if (closed) return
      closed = true
      const current = decoder
      decoder = null
      try {
        if (current && current.state !== 'closed') current.close()
      } catch {
        // 已关闭：忽略。
      }
    },
  }
}
