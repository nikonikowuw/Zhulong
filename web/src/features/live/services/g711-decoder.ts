/**
 * ITU-T G.711 解码表：A-law / μ-law 各 256 项静态查表 → 归一化 Float32 PCM。
 *
 * 为什么必须查表而不是「按公式逐样本算」：8 kHz 单声道 = 每秒 8000 次样本，
 * 每次都要移位、分支、除法。表在模块加载时构造一次，之后每个样本只有一次
 * 内存读取 —— 这条路径会被 AudioWorklet 的重采样循环逐样本调用。
 *
 * ⚠️ 表的数值不是凭记忆抄的：本文件严格按 G.711 附录里公开的参考解码
 * （Sun Microsystems `g711.c` 的 `alaw2linear` / `ulaw2linear`，被 FFmpeg、
 * 各路网关与电话栈沿用）实现，并在 `g711-decoder.test.ts` 用三条独立的结构性
 * 性质自证，任何一处抄错都会立刻失败：
 *
 *   1. 符号位翻转（`byte ^ 0x80`）必须得到相反数 —— 两张表各自 256 项全量校验；
 *   2. 静音码必须落到 0 附近（见下方 A-law 的 1 LSB 说明）；
 *   3. 幅度必须落在 G.711 的量程内（A-law ±32256，μ-law ±32124），且从不越界。
 *
 * ## 为什么 A-law 的静音码不是精确的 0
 *
 * G.711 A-law 的量化是**奇对称**的：段 0 的量化步长被刻意偏置了半个步长
 * （`+8`），因此它**没有零码**。标准静音码 `0xD5` 解出来是 `+8`、`0x55` 是 `-8`
 * （即 ±1 LSB ≈ -72 dBFS）。这是标准本身的定义，不是本表的精度问题：把 `+8`
 * 抹掉会让整条 A-law 波形带上一个直流台阶，并且与 FFmpeg 侧的解码结果对不上。
 *
 * μ-law 则是**偶对称**的：`0xFF` 与 `0x7F` 都解出精确的 0（`0x7F` 是「负零」）。
 */

const PCM_FULL_SCALE = 32768
const MULAW_BIAS = 0x84
const SIGN_BIT = 0x80
const QUANT_MASK = 0x0f
const SEG_MASK = 0x70
const SEG_SHIFT = 4

/** A-law 的字节 → 16 bit 线性样本（-32256 … +32256）。 */
function decodeALaw(byte: number): number {
  // A-law 传送时每个偶数位被取反，解码先还原（RFC 3551 §4.5.14）。
  const value = byte ^ 0x55
  const segment = (value & SEG_MASK) >> SEG_SHIFT

  let magnitude = (value & QUANT_MASK) << 4
  if (segment === 0) {
    // 段 0 是唯一带半步偏置的段：+8 让曲线在零点附近保持奇对称。
    magnitude += 8
  } else if (segment === 1) {
    magnitude += 0x108
  } else {
    magnitude += 0x108
    magnitude <<= segment - 1
  }

  return (value & SIGN_BIT) !== 0 ? magnitude : -magnitude
}

/** μ-law 的字节 → 16 bit 线性样本（-32124 … +32124）。 */
function decodeMuLaw(byte: number): number {
  const value = ~byte & 0xff
  const segment = (value & SEG_MASK) >> SEG_SHIFT
  const magnitude = (((value & QUANT_MASK) << 3) + MULAW_BIAS) << segment
  return (value & SIGN_BIT) !== 0
    ? MULAW_BIAS - magnitude
    : magnitude - MULAW_BIAS
}

function buildTable(decode: (byte: number) => number): Float32Array {
  const table = new Float32Array(256)
  for (let byte = 0; byte < 256; byte++) {
    table[byte] = decode(byte) / PCM_FULL_SCALE
  }
  return table
}

/**
 * A-law 查表（下标即码字）。
 *
 * 只读使用：TypedArray 无法 `Object.freeze`（冻结带元素的 ArrayBufferView 会抛
 * TypeError），因此约定调用方不得写入。
 */
export const ALAW_DECODE_TABLE: Float32Array = buildTable(decodeALaw)

/** μ-law 查表（下标即码字）。只读使用，同 {@link ALAW_DECODE_TABLE}。 */
export const MULAW_DECODE_TABLE: Float32Array = buildTable(decodeMuLaw)

/** 后端能力帧里的 codec 名，与 `engine.Codec.String()` 一一对应。 */
export type G711Law = 'pcma' | 'pcmu'

/** 取某个 G.711 律的查表。 */
export function g711TableFor(law: G711Law): Float32Array {
  return law === 'pcma' ? ALAW_DECODE_TABLE : MULAW_DECODE_TABLE
}

/**
 * 把一帧 G.711 负载整体解码成 Float32 PCM。
 *
 * @param law 该流的律制（来自能力帧 / `audio_info`）
 * @param payload 裸 G.711 字节（8 kHz 单声道 20 ms = 160 字节）
 * @param out 可复用的输出缓冲：长度不足时忽略并新建，避免每包一次分配
 */
export function decodeG711(
  law: G711Law,
  payload: Uint8Array,
  out?: Float32Array
): Float32Array {
  const table = g711TableFor(law)
  const samples =
    out && out.length >= payload.length ? out : new Float32Array(payload.length)
  for (let i = 0; i < payload.length; i++) {
    samples[i] = table[payload[i]]
  }
  return samples.length === payload.length
    ? samples
    : samples.subarray(0, payload.length)
}
