import {
  type FrameClass,
  type ParsedPacket,
  WIRE_FLAG_KEYFRAME,
} from '../types'

declare global {
  interface Window {
    __ZHULONG_WEB_CODECS_DIAGNOSTICS__?: boolean
  }
}

/**
 * 解码队列积压阈值（帧）。
 *
 * 12 帧 @25fps ≈ 480ms，与后端 maxClientLagUS(500ms) 同量级。
 * 超过它说明解码侧已经跟不上采集侧，继续喂帧只会让延迟单调发散。
 */
const MAX_DECODE_QUEUE_FRAMES = 12

/** 解码器刚 configure 后的预热帧数，避免启动阶段的短暂队列抖动触发重同步。 */
const WARMUP_FRAMES = 10

/**
 * 触发跳帧前所需的连续超阈值次数。
 *
 * 单次瞬时抖动（GC、rAF 长帧、主线程忙）不该触发整段重同步；
 * 连续两次都超，才说明是真的追不上。
 */
const CONGESTION_STREAK_THRESHOLD = 2

/**
 * 允许显式声明 Annex B 的配置扩展。
 *
 * `avc` 在 lib.dom.d.ts 中已存在；`hevc` 是较新的规范成员，
 * 部分 TypeScript 版本还没跟上，这里本地补齐而不污染 DOM 类型。
 */
type AnnexBDecoderConfig = VideoDecoderConfig & {
  avc?: { format: 'annexb' | 'avc' }
  hevc?: { format: 'annexb' | 'hevc' }
}

export interface StreamDecoderStats {
  /** 为保住延迟而整段丢弃到下一个 IDR 的次数 */
  resyncs: number
  /** 因参考链断裂或解码错误而重建解码器的次数 */
  rebuilds: number
  /** 解码器报错次数 */
  errors: number
}

interface DecoderDiagnosticCounters {
  inputPackets: number
  keyPackets: number
  idrClassPackets: number
  keyFlagPackets: number
  submittedChunks: number
  outputFrames: number
  waitingForKeyFrameDrops: number
  congestionDrops: number
  initialKeyFrameDrops: number
  unknownCodecDrops: number
  decoderUnavailableDrops: number
  keyFrameRecoveries: number
  configureAttempts: number
}

function emptyDiagnosticCounters(): DecoderDiagnosticCounters {
  return {
    inputPackets: 0,
    keyPackets: 0,
    idrClassPackets: 0,
    keyFlagPackets: 0,
    submittedChunks: 0,
    outputFrames: 0,
    waitingForKeyFrameDrops: 0,
    congestionDrops: 0,
    initialKeyFrameDrops: 0,
    unknownCodecDrops: 0,
    decoderUnavailableDrops: 0,
    keyFrameRecoveries: 0,
    configureAttempts: 0,
  }
}

/** 诊断开关：宿主或调试者在 window 上置 true 时输出详细事件。 */
export function isWebCodecsDiagnosticsEnabled(): boolean {
  return (
    typeof window !== 'undefined' &&
    window.__ZHULONG_WEB_CODECS_DIAGNOSTICS__ === true
  )
}

/**
 * 输出一条带统一前缀的 WebCodecs 诊断事件。
 *
 * 调用方自行判断是否已开启诊断（高频事件不要为了一行日志先做字符串拼接）。
 */
export function logWebCodecsDiagnostic(
  level: 'info' | 'warn' | 'error',
  event: string,
  details: Record<string, unknown>
): void {
  const message = `[Zhulong][WebCodecs] ${event}`
  // 诊断事件本身就是打日志：这是全模块唯一允许直连 console 的出口。
  // 用块级 disable 而不是 `next-line`：三元拆行后 `next-line` 只能盖住第一行，
  // 剩下的 console 调用会成为漏网之鱼（格式化后会静默变成 lint error）。
  /* eslint-disable no-console */
  const sink =
    level === 'error'
      ? console.error
      : level === 'warn'
        ? console.warn
        : console.info
  /* eslint-enable no-console */
  sink(message, details)
}

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
 * 反转 32 位整数的位序。
 *
 * ISO/IEC 14496-15 与 RFC 6381 规定 HEVC codec string 中的
 * general_profile_compatibility_flags 必须按**位反转**形式书写：
 * x265 Main profile 的原始值是 0x60000000，标准写法是 `hvc1.1.6.L93.B0`。
 * 直接输出原始值会得到 `hvc1.1.60000000...`，浏览器解析出的 profile 与实际不符。
 */
export function reverseBits32(value: number): number {
  let v = value >>> 0
  v = ((v & 0x55555555) << 1) | ((v >>> 1) & 0x55555555)
  v = ((v & 0x33333333) << 2) | ((v >>> 2) & 0x33333333)
  v = ((v & 0x0f0f0f0f) << 4) | ((v >>> 4) & 0x0f0f0f0f)
  v = ((v & 0x00ff00ff) << 8) | ((v >>> 8) & 0x00ff00ff)
  v = (v << 16) | (v >>> 16)
  return v >>> 0
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
          const compatRaw =
            ((payload[nalIndex + 4] << 24) |
              (payload[nalIndex + 5] << 16) |
              (payload[nalIndex + 6] << 8) |
              payload[nalIndex + 7]) >>>
            0
          const compat = reverseBits32(compatRaw).toString(16)
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

  /**
   * 跳过模式：一旦判定落后或参考链已断，就丢弃所有非关键帧直到下一个 IDR。
   *
   * ⚠️ 这与「每帧独立判断要不要丢」有本质区别：H.264 的 P 帧几乎都是参考帧，
   * 挑着丢中间某几帧会让后续帧全部解错，画面花到下一个 IDR 才恢复。
   * 整段跳到 IDR 才是唯一的正确丢法。
   */
  private skipping = false
  private needsReconfigure = false

  /** configure 之后已成功送入解码器的帧数，用于预热窗口判定。 */
  private framesSinceConfigure = 0
  /** 连续观测到的过载次数。 */
  private congestionStreak = 0
  /** 最近一次成功 configure 的单调时钟，仅用于诊断输出。 */
  private configuredAtMs: number | null = null

  private readonly onFrame: FrameOutputCallback
  private readonly onError?: DecoderErrorCallback
  private stats: StreamDecoderStats = { resyncs: 0, rebuilds: 0, errors: 0 }
  private diagnosticWindowStart = 0
  private diagnosticCounters = emptyDiagnosticCounters()
  private diagnosticLastPacket: Record<string, unknown> | null = null
  private lastDecoderInitFailureLogAt = 0

  constructor(options: StreamDecoderOptions) {
    this.onFrame = options.onFrame
    this.onError = options.onError
    this.initDecoder()
  }

  private initDecoder(): VideoDecoder | null {
    if (!isWebCodecsSupported()) {
      return null
    }

    let decoder: VideoDecoder | null = null
    try {
      decoder = new VideoDecoder({
        output: (frame: VideoFrame) => {
          if (this.decoder !== decoder) {
            frame.close()
            return
          }
          this.recordDiagnosticOutput()
          this.onFrame(frame)
        },
        error: (err: DOMException) => {
          if (this.decoder === decoder) {
            this.recoverFromDecoderError(err, 'async')
          }
        },
      })
      this.decoder = decoder
      return decoder
    } catch (e) {
      const now = Date.now()
      if (now - this.lastDecoderInitFailureLogAt >= 1000) {
        this.lastDecoderInitFailureLogAt = now
        logWebCodecsDiagnostic('error', 'decoder_init_failed', {
          errorName: e instanceof Error ? e.name : 'UnknownError',
          errorMessage: e instanceof Error ? e.message : String(e),
        })
      }
      if (this.onError && e instanceof Error) {
        this.onError(e)
      }
      return null
    }
  }

  /** 当前统计快照（供 UI 与回归测试读取）。 */
  public getStats(): StreamDecoderStats {
    return { ...this.stats }
  }

  /** 当前是否处于「等待关键帧重建」状态。 */
  public isSkipping(): boolean {
    return this.skipping
  }

  /**
   * 解码单个 ZLM2 数据包。
   *
   * @returns 是否真正送入了解码器（false 表示被丢弃）
   */
  public decode(packet: ParsedPacket): boolean {
    this.recordDiagnosticPacket(packet)

    let decoder = this.decoder
    if (!decoder || decoder.state === 'closed') {
      this.decoder = null
      decoder = this.initDecoder()
      if (!decoder || decoder.state === 'closed') {
        this.countDiagnostic('decoderUnavailableDrops')
        return false
      }
    }
    if (packet.codec === 'unknown') {
      this.countDiagnostic('unknownCodecDrops')
      this.onError?.(new Error(`Unsupported video codec: ${packet.codecByte}`))
      this.skipping = true
      return false
    }

    // 服务端标记时间轴跳变（相机重启 / RTSP 会话重建）：
    // 解码器里残留的参考帧全部作废，必须重建。等关键帧再继续。
    if (packet.hasDiscontinuity) {
      logWebCodecsDiagnostic('warn', 'stream_discontinuity', {
        seq: packet.seq,
        gopId: packet.gopId,
        codec: packet.codec,
        frameClass: packet.frameClass,
        keyFrame: packet.isKeyFrame,
        flags: packet.flags,
        hasSyntheticPts: packet.hasSyntheticPts,
        hasSyntheticDts: packet.hasSyntheticDts,
        hasInjectedParamSets: packet.hasInjectedParamSets,
        ptsUs: packet.ptsUs,
        dtsUs: packet.dtsUs,
      })
      this.rebuild()
    }

    // 跳过模式：只接受关键帧
    if (this.skipping) {
      if (!packet.isKeyFrame) {
        this.countDiagnostic('waitingForKeyFrameDrops')
        return false
      }
      this.countDiagnostic('keyFrameRecoveries')
      if (isWebCodecsDiagnosticsEnabled()) {
        logWebCodecsDiagnostic('info', 'keyframe_recovery_candidate', {
          seq: packet.seq,
          gopId: packet.gopId,
          codec: packet.codec,
          frameClass: packet.frameClass,
          keyFlag: (packet.flags & WIRE_FLAG_KEYFRAME) !== 0,
          keyFrame: packet.isKeyFrame,
          hasInjectedParamSets: packet.hasInjectedParamSets,
          decodeQueueSize: decoder.decodeQueueSize,
        })
      }
      this.skipping = false
      this.needsReconfigure = true
    }

    // 队列积压：整段跳到下一个关键帧，而不是逐帧丢参考帧。
    // 需要「预热完成 + 连续两次超阈值」才动作，避免启动瞬间的假警报。
    if (
      !packet.isKeyFrame &&
      decoder.decodeQueueSize > MAX_DECODE_QUEUE_FRAMES
    ) {
      this.congestionStreak++
      if (
        this.framesSinceConfigure > WARMUP_FRAMES &&
        this.congestionStreak >= CONGESTION_STREAK_THRESHOLD
      ) {
        this.skipping = true
        this.stats.resyncs++
        this.congestionStreak = 0
        this.countDiagnostic('congestionDrops')
        logWebCodecsDiagnostic('warn', 'decode_queue_resync', {
          seq: packet.seq,
          gopId: packet.gopId,
          codec: packet.codec,
          frameClass: packet.frameClass,
          decodeQueueSize: decoder.decodeQueueSize,
          maxDecodeQueueFrames: MAX_DECODE_QUEUE_FRAMES,
          framesSinceConfigure: this.framesSinceConfigure,
          configuredForMs:
            this.configuredAtMs === null
              ? null
              : Math.round(performance.now() - this.configuredAtMs),
          resyncs: this.stats.resyncs,
        })
        return false
      }
    } else {
      this.congestionStreak = 0
    }

    const codecStr = this.resolveCodecString(packet)

    if (
      !this.isConfigured ||
      this.configuredCodec !== codecStr ||
      this.needsReconfigure
    ) {
      // 未初始化前必须等到关键帧（参数集随关键帧到达或由服务端注入）
      if (!packet.isKeyFrame) {
        this.countDiagnostic('initialKeyFrameDrops')
        return false
      }
      if (!this.reconfigure(codecStr, packet.frameClass, packet)) {
        return false
      }
    }

    try {
      const chunk = new EncodedVideoChunk({
        type: packet.isKeyFrame ? 'key' : 'delta',
        timestamp: Math.max(0, Math.floor(packet.ptsUs)), // 微秒，服务端已归一化
        data: packet.payload,
      })
      decoder.decode(chunk)
      this.framesSinceConfigure++
      this.countDiagnostic('submittedChunks')
      return true
    } catch (err) {
      // EncodedVideoChunk 构造或入队失败：隔离旧实例，等下一个关键帧重建。
      this.recoverFromDecoderError(
        err instanceof Error ? err : new Error(String(err)),
        'decode'
      )
      return false
    }
  }

  private recordDiagnosticPacket(packet: ParsedPacket): void {
    if (!isWebCodecsDiagnosticsEnabled()) {
      this.diagnosticWindowStart = 0
      this.diagnosticCounters = emptyDiagnosticCounters()
      this.diagnosticLastPacket = null
      return
    }

    const now = Date.now()
    if (this.diagnosticWindowStart === 0) {
      this.diagnosticWindowStart = now
    } else if (now - this.diagnosticWindowStart >= 1000) {
      logWebCodecsDiagnostic('info', '1s_decoder_summary', {
        durationMs: now - this.diagnosticWindowStart,
        ...this.diagnosticCounters,
        state: this.decoder?.state ?? 'unavailable',
        decodeQueueSize: this.decoder?.decodeQueueSize ?? null,
        configured: this.isConfigured,
        configuredCodec: this.configuredCodec,
        configuredForMs:
          this.configuredAtMs === null
            ? null
            : Math.round(performance.now() - this.configuredAtMs),
        skipping: this.skipping,
        needsReconfigure: this.needsReconfigure,
        resyncs: this.stats.resyncs,
        rebuilds: this.stats.rebuilds,
        errors: this.stats.errors,
        lastPacket: this.diagnosticLastPacket,
      })
      this.diagnosticWindowStart = now
      this.diagnosticCounters = emptyDiagnosticCounters()
      this.diagnosticLastPacket = null
    }

    this.diagnosticCounters.inputPackets++
    if (packet.isKeyFrame) this.diagnosticCounters.keyPackets++
    if (packet.frameClass === 'idr') {
      this.diagnosticCounters.idrClassPackets++
    }
    if ((packet.flags & WIRE_FLAG_KEYFRAME) !== 0) {
      this.diagnosticCounters.keyFlagPackets++
    }
    this.diagnosticLastPacket = {
      seq: packet.seq,
      gopId: packet.gopId,
      codec: packet.codec,
      frameClass: packet.frameClass,
      keyFrame: packet.isKeyFrame,
      keyFlag: (packet.flags & WIRE_FLAG_KEYFRAME) !== 0,
      hasInjectedParamSets: packet.hasInjectedParamSets,
      payloadBytes: packet.payload.byteLength,
      ptsUs: packet.ptsUs,
    }
  }

  private recordDiagnosticOutput(): void {
    if (isWebCodecsDiagnosticsEnabled()) {
      this.diagnosticCounters.outputFrames++
    }
  }

  private countDiagnostic(
    counter: keyof Pick<
      DecoderDiagnosticCounters,
      | 'waitingForKeyFrameDrops'
      | 'congestionDrops'
      | 'initialKeyFrameDrops'
      | 'unknownCodecDrops'
      | 'decoderUnavailableDrops'
      | 'keyFrameRecoveries'
      | 'submittedChunks'
      | 'configureAttempts'
    >
  ): void {
    if (isWebCodecsDiagnosticsEnabled()) {
      this.diagnosticCounters[counter]++
    }
  }

  /**
   * 取得本帧应使用的 codec 字符串。
   *
   * 只在关键帧上解析参数集：非关键帧走惰性路径，避免每帧 O(N) 扫描 NAL。
   * 目标流的内联参数集若缺失，服务端会注入（PARAM_INJECTED），因此关键帧必定有 SPS。
   */
  private resolveCodecString(packet: ParsedPacket): string {
    if (
      this.isConfigured &&
      !this.needsReconfigure &&
      !packet.isKeyFrame &&
      this.configuredCodec
    ) {
      return this.configuredCodec
    }
    return packet.codec === 'h265'
      ? extractH265Codec(packet.payload)
      : extractH264Codec(packet.payload)
  }

  private reconfigure(
    codecStr: string,
    frameClass: FrameClass,
    packet: ParsedPacket
  ): boolean {
    if (!this.decoder || this.decoder.state === 'closed') {
      this.countDiagnostic('decoderUnavailableDrops')
      return false
    }

    const config = this.buildConfig(codecStr)
    this.countDiagnostic('configureAttempts')
    try {
      this.decoder.configure(config)
      this.configuredCodec = codecStr
      this.isConfigured = true
      this.needsReconfigure = false
      this.framesSinceConfigure = 0
      this.congestionStreak = 0
      this.configuredAtMs = performance.now()
      return true
    } catch (err) {
      this.stats.errors++
      this.isConfigured = false
      this.skipping = frameClass !== 'idr'
      this.configuredAtMs = null
      logWebCodecsDiagnostic('error', 'decoder_configure_failed', {
        seq: packet.seq,
        gopId: packet.gopId,
        codec: packet.codec,
        codecString: codecStr,
        frameClass: packet.frameClass,
        keyFrame: packet.isKeyFrame,
        keyFlag: (packet.flags & WIRE_FLAG_KEYFRAME) !== 0,
        hasInjectedParamSets: packet.hasInjectedParamSets,
        errorName: err instanceof Error ? err.name : 'UnknownError',
        errorMessage: err instanceof Error ? err.message : String(err),
      })
      if (this.onError && err instanceof Error) {
        this.onError(err)
      }
      return false
    }
  }

  private buildConfig(codecStr: string): AnnexBDecoderConfig {
    const config: AnnexBDecoderConfig = {
      codec: codecStr,
      optimizeForLatency: true,
    }
    // ⚠️ 规范中 AvcDecoderConfig.format 默认是 "avc"（AVCC 长度前缀），
    // 而我们送的是 Annex B（起始码分隔）。Chrome 有启发式容错，Safari 严格按规范
    // 实现会直接 DecodingError；显式声明是唯一可靠做法。HEVC 同理。
    if (codecStr.startsWith('avc1') || codecStr.startsWith('avc3')) {
      config.avc = { format: 'annexb' }
    } else if (codecStr.startsWith('hvc1') || codecStr.startsWith('hev1')) {
      config.hevc = { format: 'annexb' }
    }
    return config
  }

  /** 丢弃解码器内部状态并等待关键帧重建。 */
  private rebuild(): void {
    if (this.decoder && this.decoder.state !== 'closed') {
      try {
        this.decoder.reset()
      } catch {
        // reset 失败说明解码器已处于异常状态，交给 reconfigure 重建
      }
      this.stats.rebuilds++
    }
    this.isConfigured = false
    this.configuredCodec = null
    this.needsReconfigure = true
    this.skipping = true
    this.framesSinceConfigure = 0
    this.congestionStreak = 0
    this.configuredAtMs = null
  }

  private recoverFromDecoderError(
    error: DOMException | Error,
    source: 'async' | 'decode'
  ): void {
    this.stats.errors++
    this.stats.rebuilds++
    logWebCodecsDiagnostic('error', 'decoder_error', {
      source,
      errorName: error.name,
      errorMessage: error.message,
      state: this.decoder?.state ?? 'unavailable',
      decodeQueueSize: this.decoder?.decodeQueueSize ?? null,
      configuredCodec: this.configuredCodec,
    })
    const decoder = this.decoder
    this.decoder = null
    if (decoder && decoder.state !== 'closed') {
      try {
        decoder.close()
      } catch {
        // Ignore teardown failures; a fresh decoder is still required.
      }
    }
    this.configuredCodec = null
    this.isConfigured = false
    this.needsReconfigure = true
    this.skipping = true
    this.framesSinceConfigure = 0
    this.congestionStreak = 0
    this.configuredAtMs = null
    this.onError?.(error)
    this.initDecoder()
  }

  /** 丢弃待解码帧并等待关键帧重建（切换码流 / 重连后调用）。 */
  public reset(): void {
    this.rebuild()
  }

  public close(): void {
    const decoder = this.decoder
    this.decoder = null
    if (decoder && decoder.state !== 'closed') {
      try {
        decoder.close()
      } catch {
        // no-op
      }
    }
    this.isConfigured = false
    this.configuredCodec = null
    this.needsReconfigure = true
    this.skipping = true
    this.configuredAtMs = null
  }
}
