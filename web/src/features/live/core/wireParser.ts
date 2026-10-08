import type { CodecType, ParsedPacket } from './types';

export const WIRE_MAGIC = 0x5a4c4d31; // ASCII "ZLM1"
export const WIRE_HEADER_SIZE = 24;

export const WIRE_CODEC_UNKNOWN = 0x00;
export const WIRE_CODEC_H264 = 0x01;
export const WIRE_CODEC_H265 = 0x02;

export const WIRE_FLAG_KEYFRAME = 0x01;
export const WIRE_FLAG_HAS_PTS = 0x02;
export const WIRE_FLAG_HAS_DTS = 0x04;

export function mapCodec(codecByte: number): CodecType {
  switch (codecByte) {
    case WIRE_CODEC_H264:
      return 'h264';
    case WIRE_CODEC_H265:
      return 'h265';
    default:
      return 'unknown';
  }
}

/**
 * 解析 ZLM1 协议二进制消息。
 * 格式：24 字节固定头 + Annex B 载荷
 */
export function parseZlm1Packet(buffer: ArrayBuffer): ParsedPacket {
  if (!buffer || buffer.byteLength < WIRE_HEADER_SIZE) {
    throw new Error(`Buffer too short for ZLM1 packet: ${buffer ? buffer.byteLength : 0} < 24`);
  }

  const view = new DataView(buffer);
  const magic = view.getUint32(0, false);
  if (magic !== WIRE_MAGIC) {
    throw new Error(`Invalid ZLM1 wire magic: 0x${magic.toString(16).toUpperCase()}`);
  }

  const codecByte = view.getUint8(4);
  const flags = view.getUint8(5);
  const pts = view.getBigInt64(8, false);
  const dts = view.getBigInt64(16, false);

  const isKeyFrame = (flags & WIRE_FLAG_KEYFRAME) !== 0;
  const hasPts = (flags & WIRE_FLAG_HAS_PTS) !== 0;
  const hasDts = (flags & WIRE_FLAG_HAS_DTS) !== 0;

  // 90kHz 时钟基转换为毫秒
  const ptsMs = Number(pts) / 90;
  const dtsMs = Number(dts) / 90;

  const payload = new Uint8Array(buffer, WIRE_HEADER_SIZE);

  return {
    codec: mapCodec(codecByte),
    codecByte,
    flags,
    isKeyFrame,
    hasPts,
    hasDts,
    pts,
    dts,
    ptsMs,
    dtsMs,
    payload,
  };
}

/**
 * 从 Annex B 载荷中提取各个独立 NALU（包含起始码）。
 */
export function extractNalus(payload: Uint8Array): Uint8Array[] {
  const nalus: Uint8Array[] = [];
  const len = payload.length;
  if (len < 4) {
    if (len > 0) nalus.push(payload);
    return nalus;
  }

  const startIndices: number[] = [];
  for (let i = 0; i < len - 2; i++) {
    if (payload[i] === 0 && payload[i + 1] === 0) {
      if (payload[i + 2] === 1) {
        startIndices.push(i);
        i += 2;
      } else if (i + 3 < len && payload[i + 2] === 0 && payload[i + 3] === 1) {
        startIndices.push(i);
        i += 3;
      }
    }
  }

  if (startIndices.length === 0) {
    nalus.push(payload);
    return nalus;
  }

  for (let i = 0; i < startIndices.length; i++) {
    const start = startIndices[i];
    const end = i + 1 < startIndices.length ? startIndices[i + 1] : len;
    nalus.push(payload.subarray(start, end));
  }

  return nalus;
}
