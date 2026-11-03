/**
 * AudioSpecificConfig（ASC）解析：把 `audio_info.config` 的 base64 还原成
 * WebCodecs `AudioDecoder.configure()` 需要的那几项。
 *
 * ## 为什么必须自己解析 ASC
 *
 * RTSP 送的是**裸 AU**（没有 ADTS 头），AAC 的采样率/声道/对象类型全在 SDP 的
 * `config=` 里（`mpeg4-generic`）。`AudioDecoder` 要的是 `codec` 字符串
 * （`mp4a.40.X`）+ `description`（原始 ASC 字节），其中 `mp4a.40.X` 的 X 就是
 * `audioObjectType` 的十进制值 —— 这个值只能从 ASC 位的头 5 bit 里读出来。
 * 猜一个 `mp4a.40.2` 在本项目里是错的：AAC-LC 之外还有 HE-AAC（AOT 5）与
 * HE-AACv2（AOT 29），用错 AOT 配置解码器，Chromium 会直接
 * `NotSupportedError`，表现为「有声轨但永远没声音」。
 *
 * ## ⚠️ 采样率/声道：ASC 是唯一权威，但只在 ASC 有效时
 *
 * 后端（`native` 侧）已经保证 `audio_info.sampleRate` / `channels` **以 ASC 为准**
 * （见 `.trellis/tasks/11-03-dual-track-native/progress.md` §3：`mpeg4-generic/90000`
 * 会让 `codecpar->sample_rate` 变成 90000，所以原生层改成读 ASC）。
 *
 * 本模块仍然独立解析出 `sampleRate` / `channels`：
 *   - 两者一致时没有分歧；
 *   - 不一致时**以 ASC 为准** —— 依据同一份事实（解码器只认 ASC 描述的那套参数）；
 *   - ASC 解析失败时本函数返回 `null`，调用方**不能**退回后端字段硬配一个解码器，
 *     而应降级为「无声」。用一个来历不明的频率去 configure，得到的不是声音而是一段
 *     音调错误的噪声。
 */

/** WebCodecs 配置所需的全部信息（`description` 即原始 ASC 字节）。 */
export interface AacConfig {
  /** `mp4a.40.2` / `mp4a.40.5` / `mp4a.40.29`。 */
  codec: string
  sampleRate: number
  channels: number
  description: Uint8Array
}

/**
 * 13 项标准采样率索引表（ISO/IEC 14496-3 Table 1.18）。
 *
 * ⚠️ 索引 15 不是表内项，而是「显式频率」：后面紧跟 24 bit 的无符号整数频率。
 */
const SAMPLING_FREQUENCIES = [
  96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025,
  8000, 7350,
] as const

/** ASC 里 `samplingFrequencyIndex = 0xF` 表示后跟 24 bit 显式频率。 */
const EXPLICIT_FREQUENCY_INDEX = 0xf

/** `audioObjectType = 31` 是逃逸码：真实值 = 32 + 后续 6 bit。 */
const AOT_ESCAPE = 31

/**
 * 本切片支持的 AOT → WebCodecs codec 串。
 *
 * 其它 AOT（如 1 Main / 3 SSR / 4 LTP / 23 LD / 39 xHE-AAC）一律返回 null：
 * Chromium 的 `AudioDecoder` 只认 LC / HE-AAC / HE-AACv2 三条路径，
 * 报一个它不认识的 `mp4a.40.X` 只会在 configure 时抛 `NotSupportedError`，
 * 不如在这里就明确「播不了」。
 */
const CODEC_BY_AOT: Record<number, string> = {
  2: 'mp4a.40.2',
  5: 'mp4a.40.5',
  29: 'mp4a.40.29',
}

/**
 * ASC 的最小长度（字节）。
 *
 * `audioObjectType`(5) + `samplingFrequencyIndex`(4) + `channelConfiguration`(4)
 * 已经要 13 bit，1 个字节装不下，所以 1 字节的「ASC」必然是坏的。
 */
const MIN_ASC_BYTES = 2

/** 逐位读取器；越界读返回 0（补零而不是抛错 —— 截断的 ASC 只该被判为无效）。 */
class BitReader {
  private bitOffset = 0

  constructor(private readonly bytes: Uint8Array) {}

  read(count: number): number {
    let value = 0
    for (let i = 0; i < count; i++) {
      const byteIndex = this.bitOffset >> 3
      const bitIndex = 7 - (this.bitOffset & 7)
      const byte = byteIndex < this.bytes.length ? this.bytes[byteIndex] : 0
      value = (value << 1) | ((byte >> bitIndex) & 1)
      this.bitOffset++
    }
    return value
  }
}

/** base64 → 字节。空串 / 非法 base64 / 非浏览器环境都返回 null。 */
function decodeBase64(text: string): Uint8Array | null {
  if (!text) return null
  try {
    const binary = atob(text)
    return Uint8Array.from(binary, (char) => char.charCodeAt(0))
  } catch {
    // atob 对非法 base64 抛 InvalidCharacterError：这是不可信网络输入，静默降级。
    return null
  }
}

/**
 * 解析 base64 ASC → WebCodecs 配置；无法解析（或 AOT 不受支持）返回 `null`。
 *
 * 不抛异常：这条路径的输入来自网络，且调用方（`audio-frame-decoder`）的降级动作是
 * 「禁用音频轨、视频继续」—— 抛出去只会变成一块错误遮罩，代价与收益不成比例。
 */
export function parseAudioSpecificConfig(base64: string): AacConfig | null {
  const description = decodeBase64(base64)
  if (!description || description.length < MIN_ASC_BYTES) return null

  const reader = new BitReader(description)

  let audioObjectType = reader.read(5)
  if (audioObjectType === AOT_ESCAPE) {
    // 逃逸：真实 AOT = 32 + 6 bit。这 6 bit **必须**读掉，否则后面的
    // samplingFrequencyIndex / channelConfiguration 会整体错位 6 位。
    // 逃逸出来的值恒 >= 32，而 CODEC_BY_AOT 里三项都 < 32，因此必然落到下面的
    // `codec === undefined` → null。仍然解析完整条链路，是为了将来支持
    // xHE-AAC(42) 之类的值时只需往表里加一项。
    audioObjectType = 32 + reader.read(6)
  }

  const codec = CODEC_BY_AOT[audioObjectType]
  if (!codec) return null

  const frequencyIndex = reader.read(4)
  const sampleRate =
    frequencyIndex === EXPLICIT_FREQUENCY_INDEX
      ? reader.read(24)
      : (SAMPLING_FREQUENCIES[frequencyIndex] ?? 0)
  if (!(sampleRate > 0)) return null

  const channelConfiguration = reader.read(4)
  // 0 = 后面跟 Program Config Element（PCE）。PCE 里的声道布局要额外解析一整套
  // 语法，且 RTSP 摄像头从不下发这种形态；直接判为不支持。
  if (channelConfiguration < 1) return null

  return {
    codec,
    sampleRate,
    channels: channelConfiguration,
    description,
  }
}
