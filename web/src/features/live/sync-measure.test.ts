/**
 * 音画同步实测（父任务 AC-3 的可证伪证据）。
 *
 * 这条用例**不是**普通单测：它连一台真实后端、一路真实 RTSP 桩（`/sync` 白闪 + beep），
 * 走完整的「WS → 解封装 → WebCodecs → canvas」与「音频 WS → G.711/AAC → AudioWorklet」链路，
 * 然后把「白闪被画到画布上的墙钟时刻」与「同一媒体时刻的 beep 被排到 AudioContext 的
 * 时刻」相减。默认跳过，只在显式打开时运行（命令见
 * `.trellis/tasks/11-03-dual-track-soak/research/sync-measurement.md`）。
 *
 * ## 这条测量到底测了什么（以及没测什么）
 *
 * 测的是**渲染器这一层的调度偏差**：视频帧真正画上 canvas 的时刻，与音频帧被排进
 * AudioContext 输出时间轴的时刻之间的差。本任务真正改动的部分——两条轨道是否共用同一条
 * 视频呈现时钟、音频环是否按 `due` 投递——全部落在这个量上。
 *
 * 它**不包含**显示链路与声卡的设备固定延迟（面板扫描、DAC 缓冲）。父任务 design.md §6.3
 * 的「240 fps 录屏 + 采集卡」是对**端到端**的定义，需要真实屏幕与采集设备；在没有这些
 * 设备的环境里，把边界写清楚比伪造一个端到端数字有用：设备延迟是一个与代码无关的常数，
 * 而两条轨道的**相对**偏差才是这里要守的 80 ms。
 *
 * ## 为什么自己搭播放循环，而不是渲染 `<LivePlayer>`
 *
 * LivePlayer 要整棵路由/查询树与登录态。这里需要只是同一批 service 模块
 * （`StreamDecoder` / `PresentationScheduler` / `connectAudioChannel` /
 * `createAudioFrameDecoder` / `createLiveAudioPlayer`），它们就是产品代码本身；
 * 用几十行循环把它们串起来，比在真实页面里注入探针更可控，也不必改产品代码。
 */
import { afterAll, describe, expect, it } from 'vitest'
import {
  connectAudioChannel,
  type AudioChannel,
  type AudioChannelInfo,
  type AudioFrame,
} from './services/audio-channel'
import {
  createAudioFrameDecoder,
  type AudioFrameDecoder,
} from './services/audio-frame-decoder'
import {
  createLiveAudioPlayer,
  type AudioScheduleEvent,
  type LiveAudioPlayer,
} from './services/live-audio-player'
import { PresentationScheduler } from './services/presentation-scheduler'
import { StreamDecoder } from './services/webcodecs-decoder'
import { parseZlm2Packet } from './services/wire-parser'

const env = import.meta.env as Record<string, string | undefined>
const ENABLED = env.VITE_SYNC_MEASURE === '1'
const CAMERA = env.VITE_SYNC_CAMERA ?? 'sync'
const USERNAME = env.VITE_SYNC_USER ?? 'admin'
const PASSWORD = env.VITE_SYNC_PASSWORD ?? 'ABCd@123'
const DURATION_S = Number(env.VITE_SYNC_DURATION_S ?? 600)
/** 在这一次测量里主动断开并重连预览 WS，用来观察「视频重同步后 500 ms 内回收」。 */
const RESYNC_AT_S = Number(env.VITE_SYNC_RESYNC_AT_S ?? 120)
/** 丢弃起播的这段时间：锚点、GOP 补发、AudioContext 起播都在这里，不代表稳态。 */
const WARMUP_MS = 10_000

/** 白闪判定阈值（0..255）：黑底 → 白满幅，取中间值以抗压缩振铃。 */
const WHITE_LUMA = 200
/** beep 判定阈值（Float32 峰值）：素材是 0.5 幅度的 1 kHz 正弦。 */
const BEEP_PEAK = 0.2
/** 白闪与 beep 在媒体时间上的配对窗口：素材刻意让两者同刻，留一帧余量。 */
const PAIR_WINDOW_US = 100_000
/** 解码器就绪前（AAC 的 `isConfigSupported()` 是异步的）最多缓冲的帧数。 */
const MAX_PENDING_AUDIO_FRAMES = 50

interface FlashSample {
  ptsUs: number
  drawWallMs: number
}

interface BeepSample {
  ptsUs: number
  /** 该帧被排到的起播墙钟（`performance.now()` 域）。 */
  dueMs: number
  detectedWallMs: number
  reanchored: boolean
  /** 相对最近一次重连的毫秒数；用于「重同步后 500 ms 内回收」的分组。 */
  sinceResyncMs: number | null
}

interface SkewSample {
  mediaPtsUs: number
  skewMs: number
  sinceResyncMs: number | null
}

function median(values: number[]): number {
  if (values.length === 0) return Number.NaN
  const sorted = [...values].sort((a, b) => a - b)
  const middle = Math.floor(sorted.length / 2)
  return sorted.length % 2 === 1
    ? sorted[middle]
    : (sorted[middle - 1] + sorted[middle]) / 2
}

function percentile(values: number[], ratio: number): number {
  if (values.length === 0) return Number.NaN
  const sorted = [...values].sort((a, b) => a - b)
  const index = Math.min(
    sorted.length - 1,
    Math.max(0, Math.round(ratio * (sorted.length - 1)))
  )
  return sorted[index]
}

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    ...init,
  })
  const payload = (await response.json()) as {
    code: string
    message: string
    data: T
  }
  if (payload.code !== 'OK') {
    throw new Error(`${path} → ${payload.code}: ${payload.message}`)
  }
  return payload.data
}

async function login(): Promise<void> {
  const status = await api<{ initialized: boolean }>('/api/v1/auth/status')
  if (!status.initialized) {
    await api('/api/v1/auth/init', {
      method: 'POST',
      body: JSON.stringify({
        username: USERNAME,
        password: PASSWORD,
        confirmPassword: PASSWORD,
      }),
    })
    return
  }
  await api('/api/v1/auth/login', {
    method: 'POST',
    body: JSON.stringify({ username: USERNAME, password: PASSWORD }),
  })
}

async function resolveCameraId(): Promise<string> {
  const page = await api<{ items: Array<{ id: string; name: string }> }>(
    '/api/v1/cameras?pageSize=100'
  )
  const match = page.items.find(
    (item) => item.name === CAMERA || item.id === CAMERA
  )
  if (!match) {
    throw new Error(`camera "${CAMERA}" not found; create it before measuring`)
  }
  return match.id
}

function wsBase(): string {
  return `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}`
}

describe.skipIf(!ENABLED)('音画同步实测（白闪 + 1 kHz beep）', () => {
  const cleanup: Array<() => void | Promise<void>> = []

  afterAll(async () => {
    for (const dispose of cleanup.reverse()) {
      try {
        await dispose()
      } catch {
        // 清理失败不该掩盖测量结果
      }
    }
  })

  it(
    '稳态中位数 ≤ 80 ms、重同步后 500 ms 内回收、全程不发散',
    async () => {
      await login()
      const cameraId = await resolveCameraId()

      const canvas = document.createElement('canvas')
      const context = canvas.getContext('2d', { willReadFrequently: true })
      if (!context)
        throw new Error('2d canvas context unavailable')

        // 打开解码器诊断日志：测量失败时最怕「什么都没发生」而看不出卡在哪一层。
      ;(
        window as unknown as Record<string, unknown>
      ).__ZHULONG_WEB_CODECS_DIAGNOSTICS__ = true
      const counters = {
        binary: 0,
        parseErrors: 0,
        decodeRejected: 0,
        decodedFrames: 0,
      }

      const flashes: FlashSample[] = []
      const beeps: BeepSample[] = []
      /** 上一次投递的音频帧是否已经在 beep 里：beep 跨多帧，只在起点记一次。 */
      let wasLoud = false
      let missedBeepOnsets = 0
      let lastDrawnWhite = false
      let running = true
      let resyncAt: number | null = null
      let runningStartedAt = 0

      // ---- 视频：解码 → 呈现时钟排期 → 画布取样 ----------------------------
      const scheduler = new PresentationScheduler<VideoFrame>()
      const decoder = new StreamDecoder({
        onFrame: (frame) => {
          counters.decodedFrames++
          const pushed = scheduler.push(
            frame.timestamp,
            frame,
            performance.now()
          )
          for (const dropped of pushed.dropped) dropped.close()
        },
      })
      cleanup.push(() => decoder.close())

      let rafId = 0
      const renderLoop = (): void => {
        if (!running) return
        const now = performance.now()
        const presentation = scheduler.takeDue(now)
        for (const stale of presentation.expired) stale.close()
        const frame = presentation.present
        if (frame) {
          if (
            canvas.width !== frame.displayWidth ||
            canvas.height !== frame.displayHeight
          ) {
            canvas.width = frame.displayWidth
            canvas.height = frame.displayHeight
          }
          context.drawImage(frame, 0, 0, canvas.width, canvas.height)
          const pixel = context.getImageData(
            Math.floor(canvas.width / 2),
            Math.floor(canvas.height / 2),
            1,
            1
          ).data
          const luma = (pixel[0] + pixel[1] + pixel[2]) / 3
          const white = luma > WHITE_LUMA
          const drawWallMs = performance.now()
          if (white && !lastDrawnWhite) {
            flashes.push({ ptsUs: frame.timestamp, drawWallMs })
          }
          lastDrawnWhite = white
          frame.close()
        }
        rafId = requestAnimationFrame(renderLoop)
      }
      rafId = requestAnimationFrame(renderLoop)
      cleanup.push(() => {
        running = false
        cancelAnimationFrame(rafId)
        for (const frame of scheduler.clear()) frame.close()
      })

      // ---- 音频：通道 → 解码 → 播放器 --------------------------------------
      // 同上：这些只在回调/async 函数里赋值，用持有对象读，别让 CFA 把它们窄化成 null。
      const audio = {
        player: null as LiveAudioPlayer | null,
        info: null as AudioChannelInfo | null,
        decoder: null as AudioFrameDecoder | null,
      }
      const pendingAudio: AudioFrame[] = []
      /** 最近一次 `onScheduled`：`push()` 是同步投递，投递后即可读到本帧起播时刻。 */
      let lastScheduled: AudioScheduleEvent | null = null

      const startAudio = async (info: AudioChannelInfo): Promise<void> => {
        if (audio.player) return
        audio.info = info
        const created = createLiveAudioPlayer({
          getClock: () => scheduler.clockSnapshot(),
          onScheduled: (event) => {
            lastScheduled = event
          },
        })
        audio.player = created
        cleanup.push(() => created.close())
        await created.start()

        // 探针播放器：先看 PCM 再转给真实播放器 —— 产品代码不需要任何改动。
        const probe: Pick<LiveAudioPlayer, 'push' | 'flush'> = {
          push: (frame, pcm, inputRate) => {
            const onsetIndex = firstLoudIndex(pcm, BEEP_PEAK)
            const onset = onsetIndex >= 0 && !wasLoud
            wasLoud = onsetIndex >= 0
            const before = lastScheduled
            created.push(frame, pcm, inputRate)
            if (!onset) return
            if (!lastScheduled || lastScheduled === before) {
              // 这一帧被播放器丢掉了（陈旧 / 超前 / 环深超限）：它不会出声，
              // 记下来只会污染「应当听到的时刻」。
              missedBeepOnsets++
              return
            }
            beeps.push({
              ptsUs: frame.ptsUs,
              // 亚帧精度：beep 起点在帧内偏移也要算进去，否则每个样本自带
              // 一帧（20 ms）的量化误差，正好吃掉阈值的四分之一。
              dueMs: lastScheduled.startDueMs + (onsetIndex / inputRate) * 1000,
              detectedWallMs: performance.now(),
              reanchored: lastScheduled.reanchored,
              sinceResyncMs:
                resyncAt === null ? null : performance.now() - resyncAt,
            })
          },
          flush: () => created.flush(),
        }
        const decoder = await createAudioFrameDecoder({ info, player: probe })
        audio.decoder = decoder
        cleanup.push(() => decoder.close())
        for (const frame of pendingAudio.splice(0)) decoder.push(frame)
      }

      const channel: AudioChannel = connectAudioChannel(
        { cameraId, role: 'main' },
        {
          onInfo: (info) => {
            void startAudio(info)
          },
          onFrame: (frame) => {
            if (audio.decoder) audio.decoder.push(frame)
            else if (pendingAudio.length < MAX_PENDING_AUDIO_FRAMES) {
              pendingAudio.push(frame)
            }
          },
          onFatal: (code, reason) => {
            throw new Error(`audio channel fatal: ${code} ${reason ?? ''}`)
          },
        }
      )
      cleanup.push(() => channel.close())

      // ---- 预览 WS ---------------------------------------------------------
      // 状态放对象里：`let x: T | null = null` 只在回调里赋值时，TS 的控制流分析会把
      // 后续读取窄化成 `null`（报 "Property does not exist on type 'never'"）。
      const wire = {
        socket: null as WebSocket | null,
        info: null as { audio?: { present?: boolean } } | null,
        reconnects: 0,
      }

      const openVideo = (): void => {
        const ws = new WebSocket(
          `${wsBase()}/api/v1/cameras/${cameraId}/streams/main/ws`
        )
        ws.binaryType = 'arraybuffer'
        wire.socket = ws
        ws.onmessage = (event: MessageEvent) => {
          if (typeof event.data === 'string') {
            try {
              const message = JSON.parse(event.data) as { type?: string }
              if (message.type === 'stream_info') {
                wire.info = message as { audio?: { present?: boolean } }
              }
            } catch {
              // 非 JSON 文本帧忽略
            }
            return
          }
          counters.binary++
          try {
            const packet = parseZlm2Packet(
              new Uint8Array(event.data as ArrayBuffer)
            )
            if (!decoder.decode(packet)) counters.decodeRejected++
          } catch {
            // 单帧解封装失败不影响统计，但不能悄悄吞掉：计数进结果。
            counters.parseErrors++
          }
        }
        ws.onclose = () => {
          if (!running) return
          wire.reconnects++
          resyncAt = performance.now()
          decoder.reset()
          for (const frame of scheduler.clear()) frame.close()
          // 产品里视频重同步会让音频 flush 一次（LivePlayer 的 onResync）。
          audio.player?.flush()
          setTimeout(() => {
            if (running) openVideo()
          }, 1000)
        }
      }

      openVideo()

      // 音频通道要等视频告知「有音频」才建（与产品一致：无音轨的流不该被订阅）。
      const audioAdvertised = await (async () => {
        const deadline = performance.now() + 30_000
        while (performance.now() < deadline) {
          if (wire.info?.audio?.present) return true
          if (wire.info?.audio?.present === false) return false
          await new Promise((resolve) => setTimeout(resolve, 100))
        }
        return false
      })()
      expect(
        audioAdvertised,
        'fixture 必须宣告音轨；检查 /sync 的 SDP 与能力帧'
      ).toBe(true)

      // ---- 采样窗口 --------------------------------------------------------
      runningStartedAt = performance.now()
      const resyncPlanned = runningStartedAt + RESYNC_AT_S * 1000
      let resyncTriggered = false
      while (performance.now() - runningStartedAt < DURATION_S * 1000) {
        await new Promise((resolve) => setTimeout(resolve, 250))
        if (
          !resyncTriggered &&
          performance.now() >= resyncPlanned &&
          wire.socket
        ) {
          resyncTriggered = true
          // 断开预览 WS：解码器与呈现时钟重来，音频通道保持不动 —— 这正是
          // 「视频重同步」在产品里的形态（切码流 / 网络抖动 / 队列重建）。
          wire.socket.close()
        }
      }

      // ---- 统计 ------------------------------------------------------------
      const steadyBeeps = beeps.filter(
        (beep) => beep.detectedWallMs - runningStartedAt > WARMUP_MS
      )
      const steadyFlashes = flashes.filter(
        (flash) => flash.drawWallMs - runningStartedAt > WARMUP_MS
      )
      const skews: SkewSample[] = []
      for (const beep of steadyBeeps) {
        let nearest: FlashSample | null = null
        let nearestDistance = Number.POSITIVE_INFINITY
        for (const flash of steadyFlashes) {
          const distance = Math.abs(flash.ptsUs - beep.ptsUs)
          if (distance < nearestDistance) {
            nearest = flash
            nearestDistance = distance
          }
        }
        if (!nearest || nearestDistance > PAIR_WINDOW_US) continue
        skews.push({
          mediaPtsUs: beep.ptsUs,
          skewMs: beep.dueMs - nearest.drawWallMs,
          sinceResyncMs: beep.sinceResyncMs,
        })
      }

      const absolute = skews.map((sample) => Math.abs(sample.skewMs))
      const afterResync = skews.filter(
        (sample) => sample.sinceResyncMs !== null && sample.sinceResyncMs >= 500
      )
      const firstTenth = skews.slice(
        0,
        Math.max(1, Math.floor(skews.length / 10))
      )
      const lastTenth = skews.slice(-Math.max(1, Math.floor(skews.length / 10)))

      const summary = {
        camera: CAMERA,
        durationS: Math.round((performance.now() - runningStartedAt) / 1000),
        videoReconnects: wire.reconnects,
        counters,
        samples: {
          flashes: steadyFlashes.length,
          beeps: steadyBeeps.length,
          pairs: skews.length,
          afterResync: afterResync.length,
        },
        steadyStateMs: {
          medianAbs: median(absolute),
          p90Abs: percentile(absolute, 0.9),
          medianSigned: median(skews.map((sample) => sample.skewMs)),
        },
        afterResyncMs: {
          medianAbs: median(
            afterResync.map((sample) => Math.abs(sample.skewMs))
          ),
          maxAbs:
            afterResync.length > 0
              ? Math.max(
                  ...afterResync.map((sample) => Math.abs(sample.skewMs))
                )
              : null,
        },
        driftMs: {
          firstTenthMedian: median(firstTenth.map((sample) => sample.skewMs)),
          lastTenthMedian: median(lastTenth.map((sample) => sample.skewMs)),
          delta: Math.abs(
            median(lastTenth.map((sample) => sample.skewMs)) -
              median(firstTenth.map((sample) => sample.skewMs))
          ),
        },
        audioInfo: audio.info,
        player: audio.player?.stats ?? null,
        missedBeepOnsets,
      }
      // eslint-disable-next-line no-console
      console.info(`SYNC_MEASURE_RESULT ${JSON.stringify(summary)}`)
      // Vitest 在浏览器模式下没有可用的 console 钩子，而 `silent: 'passed-only'` 会吞掉
      // **通过**用例的输出。测量类用例的价值在于「通过了，数字是多少」，所以显式上报：
      // 用 `mode: 'no-cors'` 发简单请求，不需要被测后端支持 CORS。
      const resultUrl = env.VITE_SYNC_RESULT_URL
      if (resultUrl) {
        try {
          await fetch(resultUrl, {
            method: 'POST',
            mode: 'no-cors',
            headers: { 'Content-Type': 'text/plain' },
            body: JSON.stringify(summary),
          })
        } catch {
          // 收集器没起不该把测量判成失败：数字已经进 console 了。
        }
      }

      expect(
        skews.length,
        '配对样本不足：白闪或 beep 没被识别到'
      ).toBeGreaterThan(5)
      expect(summary.steadyStateMs.medianAbs).toBeLessThanOrEqual(80)
      if (afterResync.length > 0) {
        expect(summary.afterResyncMs.medianAbs).toBeLessThanOrEqual(80)
      }
    },
    (DURATION_S + 180) * 1000
  )
})

/** 帧内第一个超过阈值的采样下标；没有则 -1。 */
function firstLoudIndex(samples: Float32Array, threshold: number): number {
  for (let index = 0; index < samples.length; index++) {
    const value = samples[index] < 0 ? -samples[index] : samples[index]
    if (value > threshold) return index
  }
  return -1
}
