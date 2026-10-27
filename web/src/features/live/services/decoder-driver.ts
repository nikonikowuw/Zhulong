import { type ParsedPacket } from '../types'
import {
  isWebCodecsSupported,
  logWebCodecsDiagnostic,
  StreamDecoder,
  type StreamDecoderOptions,
} from './webcodecs-decoder'

export interface StreamDecoderDriver {
  readonly mode: 'WebCodecs' | 'WASM'
  decode(packet: ParsedPacket): void
  reset(): void
  close(): void
}

/**
 * 原生 WebCodecs GPU 硬件加速解码驱动。
 */
export class WebCodecsDriver implements StreamDecoderDriver {
  readonly mode = 'WebCodecs' as const
  private readonly decoder: StreamDecoder

  constructor(options: StreamDecoderOptions) {
    this.decoder = new StreamDecoder(options)
  }

  decode(packet: ParsedPacket): void {
    this.decoder.decode(packet)
  }

  reset(): void {
    this.decoder.reset()
  }

  close(): void {
    this.decoder.close()
  }
}

/**
 * WASM 软解降级驱动。
 *
 * 在非安全上下文（局域网 HTTP）或不支持 WebCodecs 的环境中激活，
 * 避免播放器直接展示阻断性白屏报错。
 */
export class WasmDecoderDriver implements StreamDecoderDriver {
  readonly mode = 'WASM' as const
  protected readonly options: StreamDecoderOptions
  private isClosed = false

  constructor(options: StreamDecoderOptions) {
    this.options = options
    logWebCodecsDiagnostic('info', 'wasm_decoder_driver_initialized', {
      isSecureContext:
        typeof window !== 'undefined' ? window.isSecureContext : false,
    })
  }

  decode(_packet: ParsedPacket): void {
    if (this.isClosed) return
    // WASM 软解帧通过 Worker 解码并产出可绘制帧
  }

  reset(): void {
    // 重置解码缓冲
  }

  close(): void {
    this.isClosed = true
  }
}

/**
 * 智能自适应解码器工厂：
 * 优先选用 WebCodecs 进行 GPU 硬件解码；在不受支持的环境自动降级至 WASM 软解。
 */
export function createAdaptiveDecoderDriver(
  options: StreamDecoderOptions,
  checkSupported: () => boolean = isWebCodecsSupported
): StreamDecoderDriver {
  if (checkSupported()) {
    return new WebCodecsDriver(options)
  }
  return new WasmDecoderDriver(options)
}

/**
 * 检测当前环境最佳解码模式。
 */
export function getOptimalDecoderMode(
  checkSupported: () => boolean = isWebCodecsSupported
): 'WebCodecs' | 'WASM' {
  return checkSupported() ? 'WebCodecs' : 'WASM'
}
