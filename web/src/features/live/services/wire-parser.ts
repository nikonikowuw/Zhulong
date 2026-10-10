import { type CodecType, type ParsedPacket } from '../types'

export const WIRE_MAGIC = 0x5a4c4d31 // ASCII "ZLM1"
const WIRE_HEADER_SIZE = 24

const WIRE_CODEC_UNKNOWN = 0x00
export const WIRE_CODEC_H264 = 0x01
export const WIRE_CODEC_H265 = 0x02

export const WIRE_FLAG_KEYFRAME = 0x01
export const WIRE_FLAG_HAS_PTS = 0x02
export const WIRE_FLAG_HAS_DTS = 0x04

function mapCodec(codecByte: number): CodecType {
  switch (codecByte) {
    case WIRE_CODEC_H264:
      return 'h264'
    case WIRE_CODEC_H265:
      return 'h265'
    case WIRE_CODEC_UNKNOWN:
    default:
      return 'unknown'
  }
}

/**
 * 零拷贝解析 ZLM1 协议二进制消息。
 * 格式：24 字节大端序固定头 + Annex B 载荷
 */
export function parseZlm1Packet(
  buffer: ArrayBuffer | Uint8Array
): ParsedPacket {
  const isU8 = buffer instanceof Uint8Array
  const totalLen = buffer.byteLength
  if (totalLen < WIRE_HEADER_SIZE) {
    throw new Error(`Buffer too short for ZLM1 packet: ${totalLen} < 24`)
  }

  const rawBuf = isU8 ? buffer.buffer : buffer
  const byteOffset = isU8 ? buffer.byteOffset : 0

  const view = new DataView(rawBuf, byteOffset, totalLen)
  const magic = view.getUint32(0, false)
  if (magic !== WIRE_MAGIC) {
    throw new Error(
      `Invalid ZLM1 wire magic: 0x${magic.toString(16).toUpperCase()}`
    )
  }

  const codecByte = view.getUint8(4)
  const flags = view.getUint8(5)
  const pts = view.getBigInt64(8, false)
  const dts = view.getBigInt64(16, false)

  const isKeyFrame = (flags & WIRE_FLAG_KEYFRAME) !== 0
  const hasPts = (flags & WIRE_FLAG_HAS_PTS) !== 0
  const hasDts = (flags & WIRE_FLAG_HAS_DTS) !== 0

  // 90kHz 时钟基转换为毫秒
  const ptsMs = Number(pts) / 90
  const dtsMs = Number(dts) / 90

  // 零拷贝直接切出 Annex B 载荷视图
  const payload = new Uint8Array(
    rawBuf,
    byteOffset + WIRE_HEADER_SIZE,
    totalLen - WIRE_HEADER_SIZE
  )

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
  }
}
