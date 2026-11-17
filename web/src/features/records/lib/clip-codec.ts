/**
 * 事件录像片段的浏览器解码能力探测。
 *
 * 片段是 fMP4/MP4 容器、视频流原样转封装（不转码），因此浏览器能否播放完全取决于
 * 它是否能解码相机的编码。H.264 几乎处处可用；H.265 只有部分浏览器 / 平台支持
 * （Chromium 需要硬件解码 + 系统解码器），不支持时前端只能提示下载。
 */

/** 后端 `codec` 字段 → 用于探测的 MIME 类型。 */
const CODEC_MIME: Record<string, string> = {
  // Constrained Baseline L3.0：最保守的 H.264 档位，能播它基本就能播相机流。
  h264: 'video/mp4; codecs="avc1.42E01E"',
  // Main L3.1，`hvc1` 标签（MP4 里参数集放在 sample entry 的写法）。
  hevc: 'video/mp4; codecs="hvc1.1.6.L93.B0"',
}

export type ClipCodecProbe = (codec: string) => boolean

/** 某个编码对应的探测 MIME；未知编码退回裸容器类型。 */
export function clipCodecMime(codec: string): string {
  return CODEC_MIME[codec.trim().toLowerCase()] ?? 'video/mp4'
}

/**
 * 默认探测：`MediaSource.isTypeSupported` 与 `video.canPlayType` 任一认可即视为支持。
 *
 * ⚠️ 两者都只是「声称支持」：真正解码失败时还要靠 `<video>` 的 `error` 事件兜底，
 * 见 RecordClipPanel。
 */
export const probeClipCodecSupport: ClipCodecProbe = (codec) => {
  const mime = clipCodecMime(codec)
  if (
    typeof MediaSource !== 'undefined' &&
    typeof MediaSource.isTypeSupported === 'function' &&
    MediaSource.isTypeSupported(mime)
  ) {
    return true
  }
  if (typeof document === 'undefined') return false
  return document.createElement('video').canPlayType(mime) !== ''
}
