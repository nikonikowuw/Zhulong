import { describe, it, expect } from 'vitest'
import {
  WIRE_FLAG_DISCONTINUITY,
  WIRE_FLAG_DTS_SYNTHETIC,
  WIRE_FLAG_KEYFRAME,
  WIRE_FLAG_PARAM_INJECTED,
  WIRE_FLAG_TIMESTAMP_SYNTHETIC,
} from '../types'
import {
  parseZlm2Packet,
  WIRE_MAGIC,
  WIRE_HEADER_SIZE,
  WIRE_PROTOCOL_VERSION,
  WIRE_CODEC_H264,
  WIRE_CODEC_H265,
} from './wire-parser'

describe('wire-parser (ZLM2)', () => {
  function buildZlm2Packet({
    magic = WIRE_MAGIC,
    version = WIRE_PROTOCOL_VERSION,
    codec = WIRE_CODEC_H264,
    frameClass = 0x01, // IDR
    flags = WIRE_FLAG_KEYFRAME,
    ptsUs = 1_000_000,
    dtsUs = 1_000_000,
    seq = 7,
    gopId = 3,
    payload = new Uint8Array([0, 0, 0, 1, 0x67, 0x42, 0xe0, 0x1e]),
  }: {
    magic?: number
    version?: number
    codec?: number
    frameClass?: number
    flags?: number
    ptsUs?: number
    dtsUs?: number
    seq?: number
    gopId?: number
    payload?: Uint8Array
  } = {}): ArrayBuffer {
    const buffer = new ArrayBuffer(WIRE_HEADER_SIZE + payload.length)
    const view = new DataView(buffer)
    view.setUint32(0, magic, false)
    view.setUint8(4, version)
    view.setUint8(5, codec)
    view.setUint8(6, frameClass)
    view.setUint8(7, flags)
    view.setBigInt64(8, BigInt(ptsUs), false)
    view.setBigInt64(16, BigInt(dtsUs), false)
    view.setUint32(24, seq, false)
    view.setUint32(28, gopId, false)

    new Uint8Array(buffer).set(payload, WIRE_HEADER_SIZE)
    return buffer
  }

  it('成功解析合法 H.264 关键帧包 (ArrayBuffer)', () => {
    const packet = parseZlm2Packet(buildZlm2Packet())

    expect(packet.codec).toBe('h264')
    expect(packet.frameClass).toBe('idr')
    expect(packet.isKeyFrame).toBe(true)
    expect(packet.ptsUs).toBe(1_000_000)
    expect(packet.dtsUs).toBe(1_000_000)
    expect(packet.seq).toBe(7)
    expect(packet.gopId).toBe(3)
    expect(packet.hasDiscontinuity).toBe(false)
    expect(packet.hasInjectedParamSets).toBe(false)
    expect(packet.payload.length).toBe(8)
    expect(packet.payload[4]).toBe(0x67)
  })

  it('成功解析 Uint8Array 输入并支持非零 byteOffset (零拷贝验证)', () => {
    const buf = buildZlm2Packet()
    const prefix = new Uint8Array([0xaa, 0xbb, 0xcc])
    const combined = new Uint8Array(prefix.length + buf.byteLength)
    combined.set(prefix, 0)
    combined.set(new Uint8Array(buf), prefix.length)

    const subView = combined.subarray(prefix.length)
    expect(subView.byteOffset).toBe(3)

    const packet = parseZlm2Packet(subView)
    expect(packet.codec).toBe('h264')
    expect(packet.isKeyFrame).toBe(true)
    expect(packet.payload.length).toBe(8)
    expect(packet.payload[4]).toBe(0x67)
  })

  it('成功解析 H.265 参考帧并识别为不可单独丢弃', () => {
    const packet = parseZlm2Packet(
      buildZlm2Packet({
        codec: WIRE_CODEC_H265,
        frameClass: 0x02, // predictive
        flags: 0,
        ptsUs: 2_000_000,
      })
    )

    expect(packet.codec).toBe('h265')
    expect(packet.frameClass).toBe('predictive')
    expect(packet.isKeyFrame).toBe(false)
    expect(packet.ptsUs).toBe(2_000_000)
  })

  it('识别非参考帧与参数集注入标记', () => {
    const packet = parseZlm2Packet(
      buildZlm2Packet({
        frameClass: 0x03, // non_reference
        flags: WIRE_FLAG_PARAM_INJECTED | WIRE_FLAG_DISCONTINUITY,
        ptsUs: 3_500_000,
      })
    )

    expect(packet.frameClass).toBe('non_reference')
    expect(packet.hasInjectedParamSets).toBe(true)
    expect(packet.hasDiscontinuity).toBe(true)
  })

  it('微秒时间戳不做任何时基换算（回归保护）', () => {
    // ZLM1 时代前端硬编码 /90 假设 90kHz；ZLM2 必须原样使用服务端归一化值。
    // 若这条断言失败，说明有人把时基换算又塞回了前端。
    const packet = parseZlm2Packet(buildZlm2Packet({ ptsUs: 33_333 }))
    expect(packet.ptsUs).toBe(33_333)
  })

  it('拒绝未知 codec，避免播放器静默按 H.264 解码', () => {
    expect(() => parseZlm2Packet(buildZlm2Packet({ codec: 0x7f }))).toThrow(
      'Unsupported ZLM2 codec'
    )
  })

  it('保留独立 DTS 并解析合成时间戳标志', () => {
    const packet = parseZlm2Packet(
      buildZlm2Packet({
        ptsUs: 120_000,
        dtsUs: 80_000,
        flags: WIRE_FLAG_TIMESTAMP_SYNTHETIC | WIRE_FLAG_DTS_SYNTHETIC,
      })
    )

    expect(packet.ptsUs).toBe(120_000)
    expect(packet.dtsUs).toBe(80_000)
    expect(packet.hasSyntheticPts).toBe(true)
    expect(packet.hasSyntheticDts).toBe(true)
  })

  it('缓冲区长度不足 32 字节时抛出异常', () => {
    expect(() => parseZlm2Packet(new ArrayBuffer(24))).toThrow(
      'Buffer too short'
    )
  })

  it('魔数不匹配时抛出异常', () => {
    expect(() =>
      parseZlm2Packet(buildZlm2Packet({ magic: 0x12345678 }))
    ).toThrow('Invalid ZLM2 wire magic')
  })

  it('协议版本不匹配时抛出异常（防止旧前端静默错解析）', () => {
    expect(() => parseZlm2Packet(buildZlm2Packet({ version: 1 }))).toThrow(
      'Unsupported ZLM2 protocol version'
    )
  })
})
