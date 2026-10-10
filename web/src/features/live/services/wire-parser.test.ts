import { describe, it, expect } from 'vitest'
import {
  parseZlm1Packet,
  WIRE_MAGIC,
  WIRE_FLAG_KEYFRAME,
  WIRE_FLAG_HAS_PTS,
  WIRE_FLAG_HAS_DTS,
  WIRE_CODEC_H264,
  WIRE_CODEC_H265,
} from './wire-parser'

describe('wire-parser', () => {
  function buildZlm1Packet({
    magic = WIRE_MAGIC,
    codec = WIRE_CODEC_H264,
    flags = WIRE_FLAG_KEYFRAME | WIRE_FLAG_HAS_PTS,
    pts = 90000n, // 1000ms
    dts = 90000n,
    payload = new Uint8Array([0, 0, 0, 1, 0x67, 0x42, 0xe0, 0x1e]),
  }: {
    magic?: number
    codec?: number
    flags?: number
    pts?: bigint
    dts?: bigint
    payload?: Uint8Array
  } = {}): ArrayBuffer {
    const buffer = new ArrayBuffer(24 + payload.length)
    const view = new DataView(buffer)
    view.setUint32(0, magic, false)
    view.setUint8(4, codec)
    view.setUint8(5, flags)
    view.setUint16(6, 0, false)
    view.setBigInt64(8, pts, false)
    view.setBigInt64(16, dts, false)

    const u8 = new Uint8Array(buffer)
    u8.set(payload, 24)
    return buffer
  }

  it('成功解析合法 H.264 关键帧包 (ArrayBuffer)', () => {
    const buf = buildZlm1Packet()
    const packet = parseZlm1Packet(buf)

    expect(packet.codec).toBe('h264')
    expect(packet.isKeyFrame).toBe(true)
    expect(packet.hasPts).toBe(true)
    expect(packet.hasDts).toBe(false)
    expect(packet.ptsMs).toBe(1000)
    expect(packet.payload.length).toBe(8)
    expect(packet.payload[4]).toBe(0x67)
  })

  it('成功解析 Uint8Array 输入并支持非零 byteOffset (零拷贝验证)', () => {
    const buf = buildZlm1Packet()
    // 构造一个包含头部前缀的更大 Uint8Array
    const prefix = new Uint8Array([0xaa, 0xbb, 0xcc])
    const combined = new Uint8Array(prefix.length + buf.byteLength)
    combined.set(prefix, 0)
    combined.set(new Uint8Array(buf), prefix.length)

    const subView = combined.subarray(prefix.length)
    expect(subView.byteOffset).toBe(3)

    const packet = parseZlm1Packet(subView)
    expect(packet.codec).toBe('h264')
    expect(packet.isKeyFrame).toBe(true)
    expect(packet.payload.length).toBe(8)
    expect(packet.payload[4]).toBe(0x67)
  })

  it('成功解析合法 H.265 非关键帧包', () => {
    const buf = buildZlm1Packet({
      codec: WIRE_CODEC_H265,
      flags: WIRE_FLAG_HAS_DTS,
      pts: 0n,
      dts: 180000n, // 2000ms
    })
    const packet = parseZlm1Packet(buf)

    expect(packet.codec).toBe('h265')
    expect(packet.isKeyFrame).toBe(false)
    expect(packet.hasPts).toBe(false)
    expect(packet.hasDts).toBe(true)
    expect(packet.dtsMs).toBe(2000)
  })

  it('缓冲区长度不足 24 字节时抛出异常', () => {
    const shortBuf = new ArrayBuffer(20)
    expect(() => parseZlm1Packet(shortBuf)).toThrow('Buffer too short')
  })

  it('魔数不匹配时抛出异常', () => {
    const badMagicBuf = buildZlm1Packet({ magic: 0x12345678 })
    expect(() => parseZlm1Packet(badMagicBuf)).toThrow(
      'Invalid ZLM1 wire magic'
    )
  })
})
