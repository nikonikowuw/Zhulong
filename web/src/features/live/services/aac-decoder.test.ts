import { describe, expect, it } from 'vitest'
import { parseAudioSpecificConfig } from './aac-decoder'

/**
 * ASC 解析的契约。
 *
 * 这里用**真实字节**而不是随手编的 hex：真实摄像机下发的 ASC 只有几组常见组合
 * （AAC-LC 8k 单声道 / 16k 单声道 / 44.1k 立体声 / 48k 立体声），
 * 一旦解析位序写错，最先错的就是这几组 —— 用它们当基准，比断言「随便一段字节能解出
 * 某个值」有用得多。
 */

/** base64 编码（浏览器 atob 的逆运算，仅测试用）。 */
function toBase64(bytes: number[]): string {
  return btoa(String.fromCharCode(...bytes))
}

describe('parseAudioSpecificConfig', () => {
  it('AAC-LC 8kHz 单声道（0x15 0x88）', () => {
    const config = parseAudioSpecificConfig('FYg=')
    expect(config).not.toBeNull()
    expect(config?.codec).toBe('mp4a.40.2')
    expect(config?.sampleRate).toBe(8000)
    expect(config?.channels).toBe(1)
    expect(Array.from(config?.description ?? [])).toEqual([0x15, 0x88])
  })

  it('AAC-LC 48kHz 立体声（0x11 0x90）', () => {
    const config = parseAudioSpecificConfig('EZA=')
    expect(config).not.toBeNull()
    expect(config?.codec).toBe('mp4a.40.2')
    expect(config?.sampleRate).toBe(48000)
    expect(config?.channels).toBe(2)
    expect(Array.from(config?.description ?? [])).toEqual([0x11, 0x90])
  })

  it('AAC-LC 44.1kHz 单声道（index 4 → 44100）', () => {
    // AOT=2(00010) | index=4(0100) | channels=1(0001) → 0001 0010 0000 1000
    const config = parseAudioSpecificConfig(toBase64([0x12, 0x08]))
    expect(config?.sampleRate).toBe(44100)
    expect(config?.channels).toBe(1)

    // 同一组位序的立体声版本：只有最后 4 bit 不同，用来钉住「声道位没读偏」。
    const stereo = parseAudioSpecificConfig(toBase64([0x12, 0x10]))
    expect(stereo?.sampleRate).toBe(44100)
    expect(stereo?.channels).toBe(2)
  })

  it('空 config 返回 null', () => {
    expect(parseAudioSpecificConfig('')).toBeNull()
  })

  it('非法 base64 返回 null（不抛）', () => {
    expect(() => parseAudioSpecificConfig('!!!not base64!!!')).not.toThrow()
    expect(parseAudioSpecificConfig('!!!not base64!!!')).toBeNull()
  })

  it('长度不足 2 字节返回 null', () => {
    // 单个字节装不下 AOT(5) + freqIndex(4) + channelConfig(4) = 13 bit
    expect(parseAudioSpecificConfig(toBase64([0x12]))).toBeNull()
  })

  it('AOT=5（HE-AAC）→ mp4a.40.5', () => {
    // AOT=5(00101) | index=3(0011 → 48000) | channels=2(0010) → 0010 1001 1001 0000
    const config = parseAudioSpecificConfig(toBase64([0x29, 0x90]))
    expect(config?.codec).toBe('mp4a.40.5')
    expect(config?.sampleRate).toBe(48000)
    expect(config?.channels).toBe(2)
  })

  it('AOT=29（HE-AACv2）→ mp4a.40.29', () => {
    // AOT=29(11101) | index=4(0100 → 44100) | channels=2(0010) → 1110 1010 0001 0000
    const config = parseAudioSpecificConfig(toBase64([0xea, 0x10]))
    expect(config?.codec).toBe('mp4a.40.29')
    expect(config?.sampleRate).toBe(44100)
    expect(config?.channels).toBe(2)
  })

  it('AOT=31 走扩展路径：32 + 6 bit（AOT=34，不支持 → null）', () => {
    // 构造：11111(escape) 000010(AOT=32+2=34) 0100(44100) 0010(2 声道)
    const config = parseAudioSpecificConfig(toBase64([0xf8, 0x48, 0x40]))
    // AOT 34 不在支持表内 → null；关键是别把后面的位读错成某个看似合法的 AOT。
    expect(config).toBeNull()
  })

  it('AOT=31 扩展路径不会把 6 bit 扩展值当采样率索引（AOT=42 → null）', () => {
    // 若实现漏读那 6 bit，freqIndex 会从扩展位里读出来（0101 → 32kHz），
    // 于是返回一个「看起来完全合法」的 config —— 正是这类假阳最难被发现。
    // 扩展值 10 → AOT=42（xHE-AAC，不支持），必须 null。
    expect(parseAudioSpecificConfig(toBase64([0xf9, 0x46, 0x40]))).toBeNull()
  })

  it('不支持的对象类型（AOT=1 Main）返回 null', () => {
    // AOT=1(00001) | index=3(0011) | channels=2(0010) → 0000 1001 1001 0000
    expect(parseAudioSpecificConfig(toBase64([0x09, 0x90]))).toBeNull()
  })

  it('sampleRateIndex=15 走 24 bit 显式频率', () => {
    // AOT=2(00010) | index=1111 | 24bit=0x00BB80(48000) | channels=2(0010)
    // 位串：00010 1111 000000001011101110000000 0010
    const config = parseAudioSpecificConfig(
      toBase64([0x17, 0x80, 0x5d, 0xc0, 0x10])
    )
    expect(config?.sampleRate).toBe(48000)
    expect(config?.channels).toBe(2)
  })

  it('channelConfiguration=0（PCE）返回 null', () => {
    // AOT=2 | index=4(44100) | channels=0
    expect(parseAudioSpecificConfig(toBase64([0x12, 0x00]))).toBeNull()
  })
})
