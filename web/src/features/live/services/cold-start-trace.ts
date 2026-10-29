/**
 * 起播冷启动追踪（Cold Start Trace）。
 *
 * 存在的理由：起播卡 1~2 秒可以来自**四个完全不同的层**，且它们的分界线在日志里
 * 天然不可见 —— 服务端在等 RTSP 握手、在等相机 IDR、在补发 GOP，浏览器在等第一个
 * 关键帧、在等解码器 configure、在等解码输出。没有分段计时就只能靠猜。
 *
 * 分两条线，各自回答一半问题：
 *
 *   [Zhulong][ColdStart] cold_start_stream
 *     ws_connected → first_packet → first_keyframe_packet
 *     （传输与「服务端到底给了什么」）
 *
 *   [Zhulong][ColdStart] cold_start_render （或 cold_start_render_stalled）
 *     first_packet → decode_submitted → frame_output → frame_drawn
 *     （解码与呈现）
 *
 * ⚠️ 刻意**不走**诊断开关：一次连接只打 1~2 行，而这类问题恰恰只能在现场复现。
 * 若哪天要静音，请加开关而不是删掉打点 —— 删掉之后同类问题会重新变成玄学。
 */

export interface PoolColdStartMarks {
  /** WebSocket 建立（或订阅到一个已连接的流）的墙钟时刻。 */
  wsConnectedAtMs: number | null
  /** 第一条二进制消息到达。 */
  firstPacketAtMs: number | null
  /** 第一条关键帧数据包到达。 */
  firstKeyframeAtMs: number | null
}

/** 相对首包的毫秒偏移；缺失的观测点返回 null，避免把「没到」误报成 0ms。 */
export function stageDeltas(marks: {
  originAtMs: number | null
  stages: Record<string, number | null>
}): Record<string, number | null> {
  const origin = marks.originAtMs
  const out: Record<string, number | null> = {}
  for (const [name, at] of Object.entries(marks.stages)) {
    out[name] = origin === null || at === null ? null : Math.round(at - origin)
  }
  return out
}

/**
 * 输出一条起播湖溯日志。
 *
 * `writeTraceLog` 是本模块唯一允许直连 console 的出口，与 webcodecs-decoder 的
 * 诊断出口同构（用块级 disable 而不是 next-line：prettier 拆行后 next-line 会盖不住）。
 */
function writeTraceLog(
  tag: 'ColdStart' | 'StreamHealth',
  event: string,
  details: Record<string, unknown>
): void {
  /* eslint-disable no-console */
  console.info(`[Zhulong][${tag}] ${event}`, details)
  /* eslint-enable no-console */
}

export function logColdStart(
  event: string,
  details: Record<string, unknown>
): void {
  writeTraceLog('ColdStart', event, details)
}

/** 首个关键帧迟迟不来时的兜底上报窗口。RTSP 冷建连实测可达 4 秒，不能报太早。 */
const KEYFRAME_WAIT_REPORT_MS = 5000

/** 稳态健康行的采样周期。 */
export const STREAM_HEALTH_INTERVAL_MS = 5000

/**
 * 输出稳态健康行。
 *
 * 起播追踪只回答「第一帧为什么慢」；「一直在顿」是另一类问题，必须看**稳态丢帧
 * 归因**：丢在呈现调度器、丢在解码器重同步，还是服务端压根没发。这三者在前端
 * 都能观测到，把它们固定成一条周期日志，同类问题就不再需要靠猜。
 */
export function logStreamHealth(details: Record<string, unknown>): void {
  writeTraceLog('StreamHealth', 'stream_health', details)
}

/** 追踪层需要的数据包视图：只取真正被读取的字段，避免与传输层结构耦合。 */
export interface PacketLike {
  isKeyFrame: boolean
  seq: number
}

export interface PoolColdStartTrace {
  observeStatus(status: string): void
  observePacket(packet: PacketLike): void
  dispose(): void
}

/**
 * 在流连接层（WebSocket 扇出）开启一次起播追踪。
 *
 * 关键观测：`packets_before_keyframe`。它一旦很大，就说明卡的是「等 IDR」而不是
 * 传输或解码 —— 这一条能把 90% 的猜测当场排除。
 */
export function createPoolColdStartTrace(options: {
  cameraId: string
  streamType: string
  now?: () => number
  report?: (event: string, details: Record<string, unknown>) => void
}): PoolColdStartTrace {
  const now = options.now ?? (() => performance.now())
  const report = options.report ?? logColdStart

  let wsConnectedAt: number | null = null
  let firstPacketAt: number | null = null
  let firstKeyframeAt: number | null = null
  let packetsBeforeKeyframe = 0
  let totalPackets = 0
  let firstPacketSeq: number | null = null
  let reported = false
  let timer: ReturnType<typeof setTimeout> | null = null

  const emit = (outcome: 'keyframe' | 'timeout' | 'dispose'): void => {
    if (reported) return
    reported = true
    if (timer !== null) {
      clearTimeout(timer)
      timer = null
    }
    report('cold_start_stream', {
      camera_id: options.cameraId,
      stream_type: options.streamType,
      outcome,
      ...stageDeltas({
        originAtMs: wsConnectedAt,
        stages: {
          first_packet_ms: firstPacketAt,
          first_keyframe_packet_ms: firstKeyframeAt,
        },
      }),
      packets_before_keyframe: packetsBeforeKeyframe,
      packets_total: totalPackets,
      first_packet_seq: firstPacketSeq,
    })
  }

  return {
    observeStatus(status) {
      if (status !== 'connected' || wsConnectedAt !== null) return
      wsConnectedAt = now()
      // 连上后迟迟没有关键帧：KEYFRAME_WAIT_REPORT_MS（5s）后主动交卷，
      // 否则这条链路上「等 IDR」永远不可见。
      timer = setTimeout(() => emit('timeout'), KEYFRAME_WAIT_REPORT_MS)
    },
    observePacket(packet) {
      totalPackets++
      if (firstPacketAt === null) {
        firstPacketAt = now()
        firstPacketSeq = packet.seq
      }
      if (packet.isKeyFrame) {
        if (firstKeyframeAt === null) {
          firstKeyframeAt = now()
          emit('keyframe')
        }
        return
      }
      if (firstKeyframeAt === null) packetsBeforeKeyframe++
    },
    dispose() {
      emit('dispose')
    },
  }
}
