import {
  type CodecType,
  type FrameClass,
  type ParsedPacket,
  WIRE_FLAG_DISCONTINUITY,
  WIRE_FLAG_DTS_SYNTHETIC,
  WIRE_FLAG_KEYFRAME,
  WIRE_FLAG_PARAM_INJECTED,
  WIRE_FLAG_TIMESTAMP_SYNTHETIC,
} from '../types'

/** ASCII "ZLM2"。 */
export const WIRE_MAGIC = 0x5a4c4d32
export const WIRE_HEADER_SIZE = 32
export const WIRE_PROTOCOL_VERSION = 2

const WIRE_CODEC_UNKNOWN = 0x00
export const WIRE_CODEC_H264 = 0x01
export const WIRE_CODEC_H265 = 0x02

const WIRE_FRAME_UNKNOWN = 0x00
const WIRE_FRAME_IDR = 0x01
const WIRE_FRAME_PREDICTIVE = 0x02
const WIRE_FRAME_NON_REFERENCE = 0x03

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

function mapFrameClass(value: number): FrameClass {
  switch (value) {
    case WIRE_FRAME_IDR:
      return 'idr'
    case WIRE_FRAME_PREDICTIVE:
      return 'predictive'
    case WIRE_FRAME_NON_REFERENCE:
      return 'non_reference'
    case WIRE_FRAME_UNKNOWN:
    default:
      return 'unknown'
  }
}

/**
 * 零拷贝解析 ZLM2 协议二进制消息。
 *
 * 格式：32 字节大端序固定头 + Annex B 载荷
 *
 * ⚠️ 这里刻意**不再**从原始 ticks 推算毫秒：ZLM1 时代前端硬编码 90kHz 时基，
 * 前端不猜源 time_base。PTS 按显示时间语义保留 B 帧重排，DTS 承载单调解码包序；二者都以微秒传递。
 */
export function parseZlm2Packet(
  buffer: ArrayBuffer | Uint8Array
): ParsedPacket {
  const isU8 = buffer instanceof Uint8Array
  const totalLen = buffer.byteLength
  if (totalLen < WIRE_HEADER_SIZE) {
    throw new Error(
      `Buffer too short for ZLM2 packet: ${totalLen} < ${WIRE_HEADER_SIZE}`
    )
  }

  const rawBuf = isU8 ? buffer.buffer : buffer
  const byteOffset = isU8 ? buffer.byteOffset : 0

  const view = new DataView(rawBuf, byteOffset, totalLen)
  const magic = view.getUint32(0, false)
  if (magic !== WIRE_MAGIC) {
    throw new Error(
      `Invalid ZLM2 wire magic: 0x${magic.toString(16).toUpperCase()}`
    )
  }

  const version = view.getUint8(4)
  if (version !== WIRE_PROTOCOL_VERSION) {
    throw new Error(`Unsupported ZLM2 protocol version: ${version}`)
  }

  const codecByte = view.getUint8(5)
  const codec = mapCodec(codecByte)
  if (codec === 'unknown') {
    throw new Error(
      `Unsupported ZLM2 codec: 0x${codecByte.toString(16).padStart(2, '0')}`
    )
  }

  const frameClassByte = view.getUint8(6)
  const flags = view.getUint8(7)
  const ptsUs = Number(view.getBigInt64(8, false))
  const dtsUs = Number(view.getBigInt64(16, false))
  const seq = view.getUint32(24, false)
  const gopId = view.getUint32(28, false)

  const frameClass = mapFrameClass(frameClassByte)
  const isKeyFrame = (flags & WIRE_FLAG_KEYFRAME) !== 0 || frameClass === 'idr'

  // 零拷贝直接切出 Annex B 载荷视图
  const payload = new Uint8Array(
    rawBuf,
    byteOffset + WIRE_HEADER_SIZE,
    totalLen - WIRE_HEADER_SIZE
  )

  return {
    codec,
    codecByte,
    flags,
    frameClass,
    isKeyFrame,
    ptsUs,
    dtsUs,
    seq,
    gopId,
    hasDiscontinuity: (flags & WIRE_FLAG_DISCONTINUITY) !== 0,
    hasInjectedParamSets: (flags & WIRE_FLAG_PARAM_INJECTED) !== 0,
    hasSyntheticPts: (flags & WIRE_FLAG_TIMESTAMP_SYNTHETIC) !== 0,
    hasSyntheticDts: (flags & WIRE_FLAG_DTS_SYNTHETIC) !== 0,
    payload,
  }
}
