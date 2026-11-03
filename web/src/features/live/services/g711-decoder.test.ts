import { describe, expect, it } from 'vitest'
import {
  ALAW_DECODE_TABLE,
  decodeG711,
  g711TableFor,
  MULAW_DECODE_TABLE,
} from './g711-decoder'

/**
 * G.711 解码表的表驱动校验。
 *
 * 这里刻意**不**逐项抄 256 个期望值：抄一遍表只能证明「我又写了一遍同样的表」，
 * 抄错一处仍然对不上。改为钉住三类独立证据：
 *   1. 协议里写死的具体码字（静音码、满幅码）；
 *   2. 全表结构性性质（奇对称 `b ^ 0x80` 取反、值域不越界）—— 256 项全量校验；
 *   3. 调用契约（复用输出缓冲、长度等于负载长度）。
 *
 * 公式只要错一处（段号、半步偏置、μ-law 的 BIAS），1 或 2 必然失败。
 */

const PCM_FULL_SCALE = 32768

/** G.711 A-law 的量程（16 bit 线性样本）。 */
const ALAW_FULL_SCALE = 32256
/** G.711 μ-law 的量程（16 bit 线性样本）。 */
const MULAW_FULL_SCALE = 32124

describe('g711-decoder', () => {
  describe('μ-law 码字', () => {
    it.each([
      // 静音码：0xFF 与 0x7F（「负零」）都解出精确 0 —— μ-law 是偶对称的。
      { name: 'silence 0xFF → 0', byte: 0xff, expected: 0 },
      { name: 'negative zero 0x7F → 0', byte: 0x7f, expected: 0 },
      // 满幅：0x00 / 0x80 是 μ-law 的两端。
      {
        name: '0x00 → -32124/32768',
        byte: 0x00,
        expected: -MULAW_FULL_SCALE / PCM_FULL_SCALE,
      },
      {
        name: '0x80 → +32124/32768',
        byte: 0x80,
        expected: MULAW_FULL_SCALE / PCM_FULL_SCALE,
      },
    ])('$name', ({ byte, expected }) => {
      expect(MULAW_DECODE_TABLE[byte]).toBeCloseTo(expected, 10)
      expect(MULAW_DECODE_TABLE[byte]).toBe(expected)
    })
  })

  describe('A-law 码字', () => {
    it.each([
      // ⚠️ A-law 是**奇对称**的，没有精确的 0 码：静音码解出 ±1 LSB（±8/32768）。
      // 这是标准定义（段 0 带半步偏置），不是精度问题 —— 把 +8 抹成 0 会引入直流台阶。
      {
        name: 'silence 0xD5 → +8/32768',
        byte: 0xd5,
        expected: 8 / PCM_FULL_SCALE,
      },
      {
        name: 'silence 0x55 → -8/32768',
        byte: 0x55,
        expected: -8 / PCM_FULL_SCALE,
      },
      {
        name: '0xAA → +32256/32768',
        byte: 0xaa,
        expected: ALAW_FULL_SCALE / PCM_FULL_SCALE,
      },
      {
        name: '0x2A → -32256/32768',
        byte: 0x2a,
        expected: -ALAW_FULL_SCALE / PCM_FULL_SCALE,
      },
    ])('$name', ({ byte, expected }) => {
      expect(ALAW_DECODE_TABLE[byte]).toBeCloseTo(expected, 10)
      expect(ALAW_DECODE_TABLE[byte]).toBe(expected)
    })
  })

  describe('全表结构性性质（256 项全量）', () => {
    it('A-law 满足奇对称：table[b ^ 0x80] === -table[b]', () => {
      for (let byte = 0; byte < 256; byte++) {
        // A-law 没有精确 0 码（奇对称，最小幅度是 ±8/32768），因此这里不需要
        // μ-law 那种 ±0 的特例，`Object.is` 级别的严格相等即可成立。
        expect(
          ALAW_DECODE_TABLE[byte ^ 0x80],
          `A-law symmetry at 0x${byte.toString(16)}`
        ).toBe(-ALAW_DECODE_TABLE[byte])
      }
    })

    it('μ-law 满足奇对称：table[b ^ 0x80] === -table[b]', () => {
      for (let byte = 0; byte < 256; byte++) {
        const mirrored = MULAW_DECODE_TABLE[byte ^ 0x80]
        const negated = -MULAW_DECODE_TABLE[byte]
        // ⚠️ 这里必须用 `===` 而不是 `Object.is`：μ-law 的两个静音码
        // （0xFF 与 0x7F）都解出 **+0**，而 `-0 === +0` 为真、`Object.is(-0, +0)` 为假。
        // 这不是表的缺陷 —— 「负零」在 G.711 里就是同一个幅度。
        expect(
          mirrored === negated,
          `μ-law symmetry at 0x${byte.toString(16)}: ${mirrored} vs ${negated}`
        ).toBe(true)
      }
    })

    it('A-law 全部落在 ±32256/32768 内', () => {
      const limit = ALAW_FULL_SCALE / PCM_FULL_SCALE
      expect(ALAW_DECODE_TABLE).toHaveLength(256)
      for (const sample of ALAW_DECODE_TABLE) {
        expect(sample).toBeGreaterThanOrEqual(-limit)
        expect(sample).toBeLessThanOrEqual(limit)
      }
    })

    it('μ-law 全部落在 ±32124/32768 内', () => {
      const limit = MULAW_FULL_SCALE / PCM_FULL_SCALE
      expect(MULAW_DECODE_TABLE).toHaveLength(256)
      for (const sample of MULAW_DECODE_TABLE) {
        expect(sample).toBeGreaterThanOrEqual(-limit)
        expect(sample).toBeLessThanOrEqual(limit)
      }
    })

    it('两张表都覆盖满 256 项且都是 Float32Array', () => {
      expect(ALAW_DECODE_TABLE).toBeInstanceOf(Float32Array)
      expect(MULAW_DECODE_TABLE).toBeInstanceOf(Float32Array)
      expect(MULAW_DECODE_TABLE).toHaveLength(256)
    })
  })

  describe('g711TableFor', () => {
    it('pcma 取 A-law 表、pcmu 取 μ-law 表（不是同一张表）', () => {
      expect(g711TableFor('pcma')).toBe(ALAW_DECODE_TABLE)
      expect(g711TableFor('pcmu')).toBe(MULAW_DECODE_TABLE)
      expect(ALAW_DECODE_TABLE).not.toBe(MULAW_DECODE_TABLE)
    })
  })

  describe('decodeG711', () => {
    it('按码字查表逐样本映射，返回值长度等于负载长度', () => {
      const payload = new Uint8Array([0xff, 0x7f, 0x00, 0x80])
      const samples = decodeG711('pcmu', payload)

      expect(samples).toHaveLength(payload.length)
      expect(Array.from(samples)).toEqual([
        MULAW_DECODE_TABLE[0xff],
        MULAW_DECODE_TABLE[0x7f],
        MULAW_DECODE_TABLE[0x00],
        MULAW_DECODE_TABLE[0x80],
      ])
    })

    it('复用传入的输出缓冲（同一实例，不重新分配）', () => {
      const payload = new Uint8Array([0xd5, 0x55, 0xaa, 0x2a])
      const out = new Float32Array(payload.length)
      const samples = decodeG711('pcma', payload, out)

      expect(samples).toBe(out)
      expect(samples).toHaveLength(payload.length)
      expect(Array.from(samples)).toEqual([
        ALAW_DECODE_TABLE[0xd5],
        ALAW_DECODE_TABLE[0x55],
        ALAW_DECODE_TABLE[0xaa],
        ALAW_DECODE_TABLE[0x2a],
      ])
    })

    it('输出缓冲过长时只写前 N 项并返回长度正确的视图', () => {
      const payload = new Uint8Array([0xff, 0x00, 0x80])
      const out = new Float32Array(160)
      const samples = decodeG711('pcmu', payload, out)

      expect(samples).toHaveLength(payload.length)
      expect(samples.buffer).toBe(out.buffer)
      expect(Array.from(samples)).toEqual(
        Array.from(payload, (byte) => MULAW_DECODE_TABLE[byte])
      )
    })

    it('输出缓冲不足时忽略它并新建，不越界写', () => {
      const payload = new Uint8Array([0xff, 0x7f, 0x00, 0x80])
      const tooSmall = new Float32Array(2)
      const samples = decodeG711('pcmu', payload, tooSmall)

      expect(samples).not.toBe(tooSmall)
      expect(samples).toHaveLength(payload.length)
      expect(Array.from(tooSmall)).toEqual([0, 0])
    })

    it('空负载返回长度 0，不是 null', () => {
      const samples = decodeG711('pcma', new Uint8Array(0))
      expect(samples).toHaveLength(0)
    })
  })
})
