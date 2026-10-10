import { describe, it, expect } from 'vitest'
import {
  extractH264Codec,
  extractH265Codec,
  isWebCodecsSupported,
} from './webcodecs-decoder'

describe('webcodecs-decoder', () => {
  it('正确检测 WebCodecs 支持环境', () => {
    // 浏览器测试环境中应当返回布尔值
    expect(typeof isWebCodecsSupported()).toBe('boolean')
  })

  it('从包含 SPS (type 7) 的 H.264 关键帧提取 codec', () => {
    // 构造包含 00 00 00 01 67 42 E0 1F 的 payload (Baseline Profile 0x42, compat 0xE0, level 0x1F = 3.1)
    const payload = new Uint8Array([
      0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0xe0, 0x1f, 0x00, 0x00, 0x00, 0x01,
      0x68, 0xce, 0x3c, 0x80,
    ])
    const codec = extractH264Codec(payload)
    expect(codec).toBe('avc1.42e01f')
  })

  it('当 H.264 payload 中无 SPS 时回退到默认 High Profile 4.0', () => {
    const payload = new Uint8Array([0x00, 0x00, 0x01, 0x65, 0x11, 0x22])
    const codec = extractH264Codec(payload)
    expect(codec).toBe('avc1.640028')
  })

  it('当 H.265 payload 中无 SPS 时回退到高兼容性 Level 5.0 (支持 1080p/2K/4K)', () => {
    const payload = new Uint8Array([0x00, 0x00, 0x00, 0x01, 0x26, 0x01])
    const codec = extractH265Codec(payload)
    expect(codec).toBe('hvc1.1.6.L150.B0')
  })

  it('从包含 SPS (type 33) 的 H.265 载荷中提取准确 profile/level', () => {
    // SPS NALU: nalType = 33 -> byte0 = 33 << 1 = 66 (0x42), byte1 = 0x01
    // followed by: ptlByte, compat (4 bytes), 6 bytes constraint, levelIdc
    const sps = new Uint8Array(18)
    sps[0] = 0x00
    sps[1] = 0x00
    sps[2] = 0x00
    sps[3] = 0x01
    sps[4] = 0x42 // nalType = 33 (SPS)
    sps[5] = 0x01 // nuh_layer_id
    sps[6] = 0x01 // vps_id etc
    sps[7] = 0x01 // ptlByte: general_profile_idc = 1 (Main Profile), tier = L
    sps[8] = 0x60 // compat byte 1
    sps[9] = 0x00
    sps[10] = 0x00
    sps[11] = 0x00
    // levelIdc is at offset 4 + 14 = 18 -> let's make buffer 20 bytes
    const fullSps = new Uint8Array(20)
    fullSps.set(sps, 0)
    fullSps[18] = 120 // Level 4.0 (1080p)

    const codec = extractH265Codec(fullSps)
    expect(codec).toContain('hvc1.1.')
    expect(codec).toContain('.L120.B0')
  })
})
