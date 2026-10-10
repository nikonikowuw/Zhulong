import { type ParsedPacket } from '../types'

export function isWebCodecsSupported(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof (window as unknown as { VideoDecoder?: unknown }).VideoDecoder !==
      'undefined' &&
    typeof (window as unknown as { EncodedVideoChunk?: unknown })
      .EncodedVideoChunk !== 'undefined'
  )
}

/**
 * 从 H.264 Annex B 关键帧的 SPS NALU 中提取 profile/level 生成标准 codec 字符串 (如 avc1.640028)
 */
export function extractH264Codec(payload: Uint8Array): string {
  const len = payload.length
  for (let i = 0; i < len - 4; i++) {
    let startCodeLen = 0
    if (payload[i] === 0 && payload[i + 1] === 0) {
      if (payload[i + 2] === 1) {
        startCodeLen = 3
      } else if (i + 3 < len && payload[i + 2] === 0 && payload[i + 3] === 1) {
        startCodeLen = 4
      }
    }

    if (startCodeLen > 0) {
      const nalIndex = i + startCodeLen
      if (nalIndex < len) {
        const nalType = payload[nalIndex] & 0x1f
        // SPS NALU type 7
        if (nalType === 7 && nalIndex + 3 < len) {
          const profile = payload[nalIndex + 1].toString(16).padStart(2, '0')
          const compat = payload[nalIndex + 2].toString(16).padStart(2, '0')
          const level = payload[nalIndex + 3].toString(16).padStart(2, '0')
          return `avc1.${profile}${compat}${level}`
        }
      }
      i += startCodeLen - 1
    }
  }
  return 'avc1.640028' // 默认 High Profile Level 4.0
}

/**
 * 从 H.265 (HEVC) Annex B 关键帧的 SPS NALU (type 33) 提取 profile/level 生成标准 codec 字符串
 */
export function extractH265Codec(payload: Uint8Array): string {
  const len = payload.length
  for (let i = 0; i < len - 5; i++) {
    let startCodeLen = 0
    if (payload[i] === 0 && payload[i + 1] === 0) {
      if (payload[i + 2] === 1) {
        startCodeLen = 3
      } else if (i + 3 < len && payload[i + 2] === 0 && payload[i + 3] === 1) {
        startCodeLen = 4
      }
    }

    if (startCodeLen > 0) {
      const nalIndex = i + startCodeLen
      if (nalIndex + 14 < len) {
        const nalType = (payload[nalIndex] >> 1) & 0x3f
        // SPS NALU type 33
        if (nalType === 33) {
          const ptlByte = payload[nalIndex + 3]
          const tierFlag = (ptlByte & 0x20) !== 0 ? 'H' : 'L'
          const profileIdc = ptlByte & 0x1f
          const compat = (
            ((payload[nalIndex + 4] << 24) |
              (payload[nalIndex + 5] << 16) |
              (payload[nalIndex + 6] << 8) |
              payload[nalIndex + 7]) >>>
            0
          )
            .toString(16)
            .toUpperCase()
          const levelIdc = payload[nalIndex + 14]
          if (profileIdc > 0 && levelIdc > 0) {
            return `hvc1.${profileIdc}.${compat}.${tierFlag}${levelIdc}.B0`
          }
        }
      }
      i += startCodeLen - 1
    }
  }
  // 默认安防高兼容性 Main Profile Level 5.0 (涵盖 1080p / 2K / 4K)
  return 'hvc1.1.6.L150.B0'
}

type FrameOutputCallback = (frame: VideoFrame) => void
type DecoderErrorCallback = (error: DOMException | Error) => void

interface StreamDecoderOptions {
  onFrame: FrameOutputCallback
  onError?: DecoderErrorCallback
}

export class StreamDecoder {
  private decoder: VideoDecoder | null = null
  private configuredCodec: string | null = null
  private isConfigured = false
  private onFrame: FrameOutputCallback
  private onError?: DecoderErrorCallback

  constructor(options: StreamDecoderOptions) {
    this.onFrame = options.onFrame
    this.onError = options.onError
    this.initDecoder()
  }

  private initDecoder(): void {
    if (!isWebCodecsSupported()) {
      return
    }

    try {
      this.decoder = new VideoDecoder({
        output: (frame: VideoFrame) => {
          this.onFrame(frame)
        },
        error: (err: DOMException) => {
          if (this.onError) {
            this.onError(err)
          }
        },
      })
    } catch (e) {
      if (this.onError && e instanceof Error) {
        this.onError(e)
      }
    }
  }

  /**
   * 解码单个 ZLM1 数据包。
   */
  public decode(packet: ParsedPacket): boolean {
    if (!this.decoder || this.decoder.state === 'closed') {
      return false
    }

    const codecStr =
      packet.codec === 'h265'
        ? extractH265Codec(packet.payload)
        : extractH264Codec(packet.payload)

    // 关键帧到来时配置或重新配置解码器
    if (!this.isConfigured || this.configuredCodec !== codecStr) {
      if (!packet.isKeyFrame) {
        // 未初始化前必须等待关键帧 (SPS/PPS)
        return false
      }
      try {
        this.decoder.configure({
          codec: codecStr,
          optimizeForLatency: true,
        })
        this.configuredCodec = codecStr
        this.isConfigured = true
      } catch (err) {
        if (this.onError && err instanceof Error) {
          this.onError(err)
        }
        return false
      }
    }

    // 队列背压：若堆积超过 6 帧，直接跳过非关键帧以保极低延迟
    if (this.decoder.decodeQueueSize > 6 && !packet.isKeyFrame) {
      return false
    }

    try {
      const chunk = new EncodedVideoChunk({
        type: packet.isKeyFrame ? 'key' : 'delta',
        timestamp: Math.max(0, Math.floor(packet.ptsMs * 1000)), // 微秒
        data: packet.payload,
      })
      this.decoder.decode(chunk)
      return true
    } catch {
      return false
    }
  }

  public reset(): void {
    if (this.decoder && this.decoder.state === 'configured') {
      try {
        this.decoder.reset()
        this.isConfigured = false
        this.configuredCodec = null
      } catch {
        // no-op
      }
    }
  }

  public close(): void {
    if (this.decoder && this.decoder.state !== 'closed') {
      try {
        this.decoder.close()
      } catch {
        // no-op
      }
      this.decoder = null
      this.isConfigured = false
      this.configuredCodec = null
    }
  }
}
