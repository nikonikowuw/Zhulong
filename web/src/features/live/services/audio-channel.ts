/**
 * 按需音频通道客户端：只负责传输，**不碰播放**。
 *
 * 为什么音频要单独一条 WebSocket，而不是塞进预览 WS：
 *   1. 预览 WS 的二进制帧必须逐字节不变 —— 旧 bundle 不能因为「多了一条音轨」黑屏；
 *   2. 音频与视频的背压语义相反：视频允许丢帧等下一个 IDR，音频丢一帧就是可闻的咔哒声；
 *   3. 静音/切格要能**立刻**停掉音频，而视频连接是 16 宫格共享的长连接。
 *
 * 本模块的边界：
 *   - 输入：WebSocket 文本帧（`audio_info` / `error`）与二进制帧（ZLA1 头 + 裸负载）；
 *   - 输出：`onInfo` / `onFrame` / `onFatal` / `onDisconnect` 四个回调；
 *   - 不做的事：建 AudioContext、解码、重采样、排期。那些都是 `live-audio-player` 的事。
 *
 * ⚠️ 不缓存历史帧。音频的价值在实时边缘，缓存只会在解禁瞬间灌进一段几十秒前的
 * 声音，听感比静音更糟。
 */
import { getCameraAudioWsUrl } from '../api/live-camera-api'

/** ZLA1 音频帧头魔数（ASCII "ZLA1"）。 */
export const AUDIO_WIRE_MAGIC = 0x5a4c4131

/** ZLA1 固定帧头长度（字节）。 */
export const AUDIO_WIRE_HEADER_SIZE = 20

/** `payload.codec`：与 C ABI 的 `Zhulong_CODEC_*` 一致。 */
export const AUDIO_CODEC_AAC = 0x10
export const AUDIO_CODEC_PCMA = 0x11
export const AUDIO_CODEC_PCMU = 0x12

/** 帧头 `flags` 位：源流时间轴跳变（或视频侧重锚）。 */
export const AUDIO_FLAG_DISCONTINUITY = 0x01
/** 帧头 `flags` 位：PTS 由原生层按包长/帧长递推合成。 */
export const AUDIO_FLAG_TIMESTAMP_SYNTHETIC = 0x02

/** 后端应用自定义关闭码（`internal/camera/audio_channel.go`）。 */
export const AUDIO_CLOSE_SUPERSEDED = 4001
export const AUDIO_CLOSE_TOO_MANY_CHANNELS = 4002
export const AUDIO_CLOSE_BACKPRESSURE = 4003
export const AUDIO_CLOSE_NO_AUDIO = 4404
export const AUDIO_CLOSE_INTERNAL = 4500

/** 首帧退避（毫秒）。之后逐次翻倍，封顶 {@link MAX_BACKOFF_MS}。 */
const BASE_BACKOFF_MS = 100
/** 退避上限（毫秒）。音频是「点了喇叭就该有声」的交互，超过 1s 的等待比断线本身更明显。 */
const MAX_BACKOFF_MS = 1000

const AUDIO_INFO_TYPE = 'audio_info'
const AUDIO_ERROR_TYPE = 'error'

export interface AudioChannelInfo {
  codec: 'aac' | 'pcma' | 'pcmu'
  sampleRate: number
  channels: number
  /** base64 AudioSpecificConfig；G.711 为空串 */
  config: string
}

export interface AudioFrame {
  codec: number // 16=AAC 17=PCMA 18=PCMU
  flags: number // bit0 discontinuity, bit1 synthetic pts
  seq: number
  /** int64 已由原生层归一化，前端不得重算 */
  ptsUs: number
  payload: Uint8Array
}

export interface AudioChannelHandlers {
  onInfo: (info: AudioChannelInfo) => void
  onFrame: (frame: AudioFrame) => void
  /** 终止性错误：调用后不得重连 */
  onFatal: (code: string, reason?: string) => void
  /** 可恢复断开（网络/4001/4002/4003/4500），由本类自行退避重连 */
  onDisconnect?: (code: number) => void
}

export interface AudioChannel {
  close(): void
  readonly state: 'connecting' | 'open' | 'closed'
  /** 已丢弃的畸形二进制帧数（长度不足 / magic 不符）。仅本地诊断用。 */
  readonly droppedFrames: number
}

export interface AudioChannelParams {
  cameraId: string
  role: 'main' | 'sub'
}

/**
 * 把 `stream_info` / `audio_info` 里的 codec 串收敛到三种可播放的律制。
 *
 * 只认小写三值：后端 `engine.Codec.String()` 的其余输出（`h264`、`codec(99)`…）
 * 前端能做的就是「这条音轨播不了」，把它当作未知而不是猜。
 */
function normalizeCodec(raw: unknown): AudioChannelInfo['codec'] | null {
  if (raw === 'aac' || raw === 'pcma' || raw === 'pcmu') return raw
  return null
}

/** 解析 `audio_info` 文本帧；不合规返回 null（静默忽略，不动状态）。 */
function parseAudioInfo(raw: string): AudioChannelInfo | null {
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return null
  }
  if (typeof parsed !== 'object' || parsed === null) return null

  const candidate = parsed as {
    type?: unknown
    codec?: unknown
    sampleRate?: unknown
    channels?: unknown
    config?: unknown
    hasAudio?: unknown
  }
  if (candidate.type !== AUDIO_INFO_TYPE) return null
  // hasAudio=false 说明后端认为这条连接不该有声音：不当作 info，让连接自然走关闭码。
  if (candidate.hasAudio === false) return null

  const codec = normalizeCodec(candidate.codec)
  if (!codec) return null

  return {
    codec,
    sampleRate:
      typeof candidate.sampleRate === 'number' ? candidate.sampleRate : 0,
    channels: typeof candidate.channels === 'number' ? candidate.channels : 0,
    config: typeof candidate.config === 'string' ? candidate.config : '',
  }
}

/** 解析 `error` 文本帧；不合规返回 null。 */
function parseAudioError(
  raw: string
): { code: string; reason?: string } | null {
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return null
  }
  if (typeof parsed !== 'object' || parsed === null) return null

  const candidate = parsed as {
    type?: unknown
    code?: unknown
    reason?: unknown
  }
  if (candidate.type !== AUDIO_ERROR_TYPE) return null
  if (typeof candidate.code !== 'string' || !candidate.code) return null

  return {
    code: candidate.code,
    ...(typeof candidate.reason === 'string' && candidate.reason
      ? { reason: candidate.reason }
      : {}),
  }
}

/**
 * 解析 ZLA1 二进制帧。
 *
 * 失败返回 null —— 不抛。这条路径每秒跑 50 次，一个畸形包把异常抛到 UI 层，
 * 换来的是一整块预览格的错误遮罩，代价与收益完全不成比例。
 *
 * ⚠️ `pts_us` 是 **int64**。用 `getUint32` 只取低 32 位会在 71.6 分钟后静默回绕，
 * 表现为「音频时钟突然跳回起点」；用 `getBigInt64` 再 `Number()` 才能拿到真值
 * （微秒在 double 的安全整数范围内，直到约 285 年）。
 */
export function parseAudioFrame(buffer: ArrayBuffer): AudioFrame | null {
  if (buffer.byteLength < AUDIO_WIRE_HEADER_SIZE) return null

  const view = new DataView(buffer)
  if (view.getUint32(0, false) !== AUDIO_WIRE_MAGIC) return null

  return {
    codec: view.getUint8(4),
    flags: view.getUint8(5),
    // 6..7 reserved
    seq: view.getUint32(8, false),
    ptsUs: Number(view.getBigInt64(12, false)),
    payload: new Uint8Array(buffer, AUDIO_WIRE_HEADER_SIZE),
  }
}

/**
 * 建立一条按需音频通道。
 *
 * 生命周期：`connecting` →（`audio_info`）→ `open` →「可恢复断开 → 退避重连」或 `close()`。
 * 一旦进入 `closed` 就不再有任何 WebSocket 或定时器存活。
 */
export function connectAudioChannel(
  params: AudioChannelParams,
  handlers: AudioChannelHandlers
): AudioChannel {
  const url = getCameraAudioWsUrl(params.cameraId, params.role)

  let ws: WebSocket | null = null
  let state: AudioChannel['state'] = 'connecting'
  let retries = 0
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null

  /**
   * 唯一的「已关闭」判据。
   *
   * 不能只看 `ws.readyState`：`close()` 之后旧 socket 的 `onclose` 仍会异步到达，
   * 若在回调里靠 readyState 判断，会看到 CLOSED 却分不清「对端关的」还是「我关的」，
   * 于是照样安排重连 —— 用户点了静音，声音过一秒又回来了。
   */
  let closed = false

  /** 解析失败计数。普通 number，不引入遥测系统：这是本地诊断量，不是业务指标。 */
  let droppedFrames = 0

  function clearReconnect(): void {
    if (reconnectTimer !== null) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
  }

  /** 终止：调用后不得再有任何重连。 */
  function terminate(): void {
    closed = true
    state = 'closed'
    clearReconnect()
  }

  function scheduleReconnect(): void {
    if (closed) return

    retries++
    const base = Math.min(
      BASE_BACKOFF_MS * Math.pow(2, retries - 1),
      MAX_BACKOFF_MS
    )
    // 抖动：多格同时断线时不至于同拍去敲门（与视频池一致的取舍，但上限短得多）。
    const delay = Math.round(base * (0.5 + Math.random() * 0.5))

    clearReconnect()
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null
      if (closed) return
      open()
    }, delay)
  }

  function handleText(raw: string): void {
    const info = parseAudioInfo(raw)
    if (info) {
      state = 'open'
      retries = 0
      handlers.onInfo(info)
      return
    }

    const error = parseAudioError(raw)
    if (error) {
      // 置灰语义：后端已经明确说「这路给不了声音」，重连只会变成报错循环。
      terminate()
      const socket = ws
      ws = null
      socket?.close()
      handlers.onFatal(error.code, error.reason)
    }
    // 其余文本帧（未知 type / 非 JSON）：静默忽略，不动任何状态。
  }

  function handleBinary(buffer: ArrayBuffer): void {
    const frame = parseAudioFrame(buffer)
    if (!frame) {
      droppedFrames++
      return
    }
    handlers.onFrame(frame)
  }

  function open(): void {
    if (closed) return

    state = 'connecting'
    let socket: WebSocket
    try {
      socket = new WebSocket(url)
    } catch {
      // 构造失败（非法 URL / 被策略拦截）也算可恢复：退避后再试，而不是把 UI 钉死。
      scheduleReconnect()
      return
    }

    socket.binaryType = 'arraybuffer'
    ws = socket

    socket.onmessage = (event: MessageEvent) => {
      if (closed) return
      if (typeof event.data === 'string') {
        handleText(event.data)
        return
      }
      if (event.data instanceof ArrayBuffer) {
        handleBinary(event.data)
      }
    }

    socket.onclose = (event: CloseEvent) => {
      // 旧 socket 的 onclose 可能在新 socket 建好之后才到：只处理当前这一条。
      if (ws === socket) ws = null
      if (closed) return

      state = 'closed'
      const code = event.code

      if (code === AUDIO_CLOSE_NO_AUDIO) {
        terminate()
        handlers.onFatal('NO_AUDIO', 'camera reports no playable audio track')
        return
      }

      // 4001/4002/4003/4500 是「被顶替 / 被限流 / 被背压 / 内部错误」。
      // 它们都不是「这路没有声音」，所以可恢复：告知一次，然后照常退避重连。
      // 只有 4404 与 `error` 文本帧才是终止性的。
      if (
        code === AUDIO_CLOSE_SUPERSEDED ||
        code === AUDIO_CLOSE_TOO_MANY_CHANNELS ||
        code === AUDIO_CLOSE_BACKPRESSURE ||
        code === AUDIO_CLOSE_INTERNAL
      ) {
        handlers.onDisconnect?.(code)
      }

      scheduleReconnect()
    }

    // onerror 不携带任何可用信息（浏览器出于安全考虑固定给空事件），
    // 关闭一定紧跟在 onerror 之后到达，所以状态迁移全部交给 onclose。
  }

  open()

  return {
    close(): void {
      if (closed) return
      terminate()
      const socket = ws
      ws = null
      socket?.close()
      // 防御：某些实现下 close() 不会同步触发 onclose，这里不依赖它。
      retries = 0
    },
    get state() {
      return state
    },
    get droppedFrames() {
      return droppedFrames
    },
  }
}
