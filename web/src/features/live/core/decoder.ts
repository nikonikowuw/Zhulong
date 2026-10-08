import type { ParsedPacket } from './types';

export function isWebCodecsSupported(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof (window as unknown as { VideoDecoder?: unknown }).VideoDecoder !== 'undefined' &&
    typeof (window as unknown as { EncodedVideoChunk?: unknown }).EncodedVideoChunk !== 'undefined'
  );
}

export type FrameOutputCallback = (frame: VideoFrame) => void;
export type DecoderErrorCallback = (error: DOMException | Error) => void;

export interface StreamDecoderOptions {
  onFrame: FrameOutputCallback;
  onError?: DecoderErrorCallback;
}

export class StreamDecoder {
  private decoder: VideoDecoder | null = null;
  private configuredCodec: string | null = null;
  private isConfigured = false;
  private onFrame: FrameOutputCallback;
  private onError?: DecoderErrorCallback;

  constructor(options: StreamDecoderOptions) {
    this.onFrame = options.onFrame;
    this.onError = options.onError;
    this.initDecoder();
  }

  private initDecoder(): void {
    if (!isWebCodecsSupported()) {
      return;
    }

    try {
      this.decoder = new VideoDecoder({
        output: (frame: VideoFrame) => {
          this.onFrame(frame);
        },
        error: (err: DOMException) => {
          if (this.onError) {
            this.onError(err);
          }
        },
      });
    } catch (e) {
      if (this.onError && e instanceof Error) {
        this.onError(e);
      }
    }
  }

  /**
   * 解码单个 ZLM1 数据包。
   */
  public decode(packet: ParsedPacket): boolean {
    if (!this.decoder || this.decoder.state === 'closed') {
      return false;
    }

    const codecStr = packet.codec === 'h265' ? 'hvc1.1.6.L93.B0' : 'avc1.640028';

    // 关键帧到来时配置或重新配置解码器
    if (!this.isConfigured || this.configuredCodec !== codecStr) {
      if (!packet.isKeyFrame) {
        // 未初始化前必须等待关键帧 (SPS/PPS)
        return false;
      }
      try {
        this.decoder.configure({
          codec: codecStr,
          optimizeForLatency: true,
        });
        this.configuredCodec = codecStr;
        this.isConfigured = true;
      } catch (err) {
        if (this.onError && err instanceof Error) {
          this.onError(err);
        }
        return false;
      }
    }

    // 队列背压：若堆积超过 6 帧，直接跳过以保低延迟
    if (this.decoder.decodeQueueSize > 6 && !packet.isKeyFrame) {
      return false;
    }

    try {
      const chunk = new EncodedVideoChunk({
        type: packet.isKeyFrame ? 'key' : 'delta',
        timestamp: Math.max(0, Math.floor(packet.ptsMs * 1000)), // 微秒
        data: packet.payload,
      });
      this.decoder.decode(chunk);
      return true;
    } catch {
      return false;
    }
  }

  public reset(): void {
    if (this.decoder && this.decoder.state === 'configured') {
      try {
        this.decoder.reset();
        this.isConfigured = false;
        this.configuredCodec = null;
      } catch {
        // no-op
      }
    }
  }

  public close(): void {
    if (this.decoder && this.decoder.state !== 'closed') {
      try {
        this.decoder.close();
      } catch {
        // no-op
      }
      this.decoder = null;
      this.isConfigured = false;
      this.configuredCodec = null;
    }
  }
}
