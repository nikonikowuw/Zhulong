import { describe, it, expect, vi } from 'vitest'
import {
  extractH264Codec,
  extractH265Codec,
  getWebCodecsSupportIssue,
  isWebCodecsSupported,
  reverseBits32,
  StreamDecoder,
} from './webcodecs-decoder'

describe('webcodecs-decoder', () => {
  it('正确检测 WebCodecs 支持环境', () => {
    // 浏览器测试环境中应当返回布尔值
    expect(typeof isWebCodecsSupported()).toBe('boolean')
    // 在支持环境下 issue 应为 null
    if (isWebCodecsSupported()) {
      expect(getWebCodecsSupportIssue()).toBeNull()
    }
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

  it('H.265 compatibility 字段按 ISO 14496-15 做位反转 (回归保护)', () => {
    // x265 Main profile 的 general_profile_compatibility_flags 原始值是
    // 0x60000000，但 codec string 里必须写成位反转后的 6。
    // 直接输出原值会得到 hvc1.1.60000000...，浏览器解析出的 profile 与实际不符。
    const sps = new Uint8Array(20)
    sps[0] = 0x00
    sps[1] = 0x00
    sps[2] = 0x00
    sps[3] = 0x01
    sps[4] = 0x42 // nalType = 33 (SPS)
    sps[5] = 0x01
    sps[6] = 0x01
    sps[7] = 0x01 // profile_idc = 1, tier = L
    sps[8] = 0x60 // compat 0x60000000
    sps[9] = 0x00
    sps[10] = 0x00
    sps[11] = 0x00
    sps[18] = 120 // Level 4.0

    expect(extractH265Codec(sps)).toBe('hvc1.1.6.L120.B0')
  })

  it('reverseBits32 对本项目涉及的 profile 位序正确', () => {
    expect(reverseBits32(0x60000000)).toBe(0x00000006)
    expect(reverseBits32(0x00000000)).toBe(0)
    expect(reverseBits32(0xffffffff)).toBe(0xffffffff)
    expect(reverseBits32(0x80000000)).toBe(0x00000001)
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

describe('StreamDecoder 重同步语义', () => {
  function packet({
    isKeyFrame,
    frameClass = isKeyFrame ? 'idr' : 'predictive',
    ptsUs,
    rawKey = isKeyFrame,
    discontinuity = false,
  }: {
    isKeyFrame: boolean
    frameClass?: 'unknown' | 'idr' | 'predictive' | 'non_reference'
    ptsUs: number
    rawKey?: boolean
    discontinuity?: boolean
  }) {
    return {
      codec: 'h264' as const,
      codecByte: 1,
      flags: 0,
      frameClass,
      isKeyFrame: rawKey,
      ptsUs,
      dtsUs: ptsUs,
      seq: 0,
      gopId: 1,
      hasDiscontinuity: discontinuity,
      hasInjectedParamSets: true,
      hasSyntheticPts: false,
      hasSyntheticDts: false,
      payload: new Uint8Array([0, 0, 0, 1, 0x67, 0x42, 0xe0, 0x1f]),
    }
  }

  it('异步解码错误后重建 VideoDecoder 并在下一个 IDR 恢复', () => {
    class FakeVideoDecoder {
      static instances: FakeVideoDecoder[] = []
      state: 'unconfigured' | 'configured' | 'closed' = 'unconfigured'
      decodeQueueSize = 0
      decodeCount = 0

      constructor(private readonly callbacks: VideoDecoderInit) {
        FakeVideoDecoder.instances.push(this)
      }

      configure(_config: VideoDecoderConfig) {
        this.state = 'configured'
      }

      decode(_chunk: EncodedVideoChunk) {
        if (this.state !== 'configured')
          throw new Error('decoder not configured')
        this.decodeCount++
      }

      reset() {
        this.state = 'unconfigured'
      }

      close() {
        this.state = 'closed'
      }

      fail() {
        this.state = 'closed'
        this.callbacks.error(new DOMException('decode failed', 'EncodingError'))
      }
    }

    class FakeEncodedVideoChunk {
      constructor(_init: EncodedVideoChunkInit) {}
    }

    vi.stubGlobal('VideoDecoder', FakeVideoDecoder)
    vi.stubGlobal('EncodedVideoChunk', FakeEncodedVideoChunk)
    FakeVideoDecoder.instances = []

    const decoder = new StreamDecoder({ onFrame: () => {} })
    try {
      const keyframe = packet({ isKeyFrame: true, ptsUs: 0 })
      const delta = packet({ isKeyFrame: false, ptsUs: 40_000 })
      expect(decoder.decode(keyframe)).toBe(true)
      expect(FakeVideoDecoder.instances).toHaveLength(1)

      FakeVideoDecoder.instances[0].fail()
      expect(decoder.isSkipping()).toBe(true)
      expect(FakeVideoDecoder.instances).toHaveLength(2)
      expect(decoder.decode(delta)).toBe(false)
      expect(decoder.decode(packet({ isKeyFrame: true, ptsUs: 80_000 }))).toBe(
        true
      )
      expect(FakeVideoDecoder.instances[1].decodeCount).toBe(1)
      expect(decoder.getStats().errors).toBe(1)
    } finally {
      decoder.close()
      vi.unstubAllGlobals()
    }
  })

  it('拒绝未知 codec，避免以 H.264 配置送入解码器', () => {
    const decoder = new StreamDecoder({ onFrame: () => {} })
    try {
      expect(
        decoder.decode({
          ...packet({ isKeyFrame: true, ptsUs: 0 }),
          codec: 'unknown',
          codecByte: 0x7f,
        })
      ).toBe(false)
    } finally {
      decoder.close()
    }
  })

  it('未配置时丢弃非关键帧，等待关键帧', () => {
    const decoder = new StreamDecoder({ onFrame: () => {} })
    try {
      expect(decoder.decode(packet({ isKeyFrame: false, ptsUs: 1000 }))).toBe(
        false
      )
      expect(decoder.isSkipping()).toBe(false)
    } finally {
      decoder.close()
    }
  })

  it('debug mode reports packet classification and decoder flow once per second', () => {
    class FakeVideoDecoder {
      state: 'unconfigured' | 'configured' | 'closed' = 'unconfigured'
      decodeQueueSize = 0

      constructor(_callbacks: VideoDecoderInit) {}

      configure(_config: VideoDecoderConfig) {
        this.state = 'configured'
      }

      decode(_chunk: EncodedVideoChunk) {}

      reset() {
        this.state = 'unconfigured'
      }

      close() {
        this.state = 'closed'
      }
    }

    class FakeEncodedVideoChunk {
      constructor(_init: EncodedVideoChunkInit) {}
    }

    const previousDiagnostics = window.__ZHULONG_WEB_CODECS_DIAGNOSTICS__
    const infoSpy = vi.spyOn(console, 'info').mockImplementation(() => {})
    const nowSpy = vi.spyOn(Date, 'now')
    vi.stubGlobal('VideoDecoder', FakeVideoDecoder)
    vi.stubGlobal('EncodedVideoChunk', FakeEncodedVideoChunk)
    window.__ZHULONG_WEB_CODECS_DIAGNOSTICS__ = true

    const decoder = new StreamDecoder({ onFrame: () => {} })
    try {
      nowSpy.mockReturnValue(1000)
      expect(decoder.decode(packet({ isKeyFrame: true, ptsUs: 0 }))).toBe(true)

      nowSpy.mockReturnValue(2001)
      expect(decoder.decode(packet({ isKeyFrame: false, ptsUs: 40_000 }))).toBe(
        true
      )

      const summary = infoSpy.mock.calls.find(
        ([event]) => event === '[Zhulong][WebCodecs] 1s_decoder_summary'
      )?.[1]
      expect(summary).toMatchObject({
        inputPackets: 1,
        keyPackets: 1,
        idrClassPackets: 1,
        keyFlagPackets: 0,
        submittedChunks: 1,
        outputFrames: 0,
        lastPacket: { frameClass: 'idr', keyFrame: true, keyFlag: false },
      })
    } finally {
      decoder.close()
      if (previousDiagnostics === undefined) {
        delete window.__ZHULONG_WEB_CODECS_DIAGNOSTICS__
      } else {
        window.__ZHULONG_WEB_CODECS_DIAGNOSTICS__ = previousDiagnostics
      }
      infoSpy.mockRestore()
      nowSpy.mockRestore()
      vi.unstubAllGlobals()
    }
  })

  it('队列失控时重建解码器：必须 reset 清空待解码队列，而不是只打跳过标记', () => {
    /**
     * 回归保护：曾经的重同步只置 `skipping` 标记，没有 reset()，
     * 于是 WebCodecs 内部的待解码 chunk 仍被逐个解出来 —— 现场实测
     * decodeQueueSize=262、resyncs=11：客户端永久落后十几秒且每秒复发。
     */
    class FakeVideoDecoder {
      static last: FakeVideoDecoder | null = null
      state: 'unconfigured' | 'configured' | 'closed' = 'unconfigured'
      decodeQueueSize = 0
      resetCalls = 0

      constructor(_callbacks: VideoDecoderInit) {
        FakeVideoDecoder.last = this
      }

      configure(_config: VideoDecoderConfig) {
        this.state = 'configured'
      }

      decode(_chunk: EncodedVideoChunk) {}

      reset() {
        this.resetCalls++
        this.decodeQueueSize = 0
        this.state = 'unconfigured'
      }

      close() {
        this.state = 'closed'
      }
    }

    class FakeEncodedVideoChunk {
      constructor(_init: EncodedVideoChunkInit) {}
    }

    vi.stubGlobal('VideoDecoder', FakeVideoDecoder)
    vi.stubGlobal('EncodedVideoChunk', FakeEncodedVideoChunk)

    const decoder = new StreamDecoder({ onFrame: () => {} })
    try {
      expect(decoder.decode(packet({ isKeyFrame: true, ptsUs: 0 }))).toBe(true)
      const fake = FakeVideoDecoder.last
      expect(fake).not.toBeNull()

      // 超过硬上限（64 帧）→ 立刻整段放弃，不必等持续 1 秒
      fake!.decodeQueueSize = 262
      expect(decoder.decode(packet({ isKeyFrame: false, ptsUs: 40_000 }))).toBe(
        false
      )

      expect(fake!.resetCalls).toBeGreaterThan(0)
      expect(decoder.getStats().resyncs).toBe(1)
      expect(decoder.isSkipping()).toBe(true)
      // 队列峰值可被读取并清零，供稳态健康行判断「吞吐不够 vs 成簇到达」
      expect(decoder.takeDecodeQueuePeak()).toBe(262)
      expect(decoder.takeDecodeQueuePeak()).toBe(0)
    } finally {
      decoder.close()
      vi.unstubAllGlobals()
    }
  })

  it('时间轴跳变后必须等待关键帧（不能继续喂 delta 帧）', () => {
    const decoder = new StreamDecoder({ onFrame: () => {} })
    try {
      decoder.decode(packet({ isKeyFrame: true, ptsUs: 0 }))
      decoder.decode(
        packet({ isKeyFrame: false, ptsUs: 40000, discontinuity: true })
      )

      expect(decoder.isSkipping()).toBe(true)
      expect(decoder.decode(packet({ isKeyFrame: false, ptsUs: 80000 }))).toBe(
        false
      )
      expect(decoder.getStats().rebuilds).toBeGreaterThan(0)
    } finally {
      decoder.close()
    }
  })
})
