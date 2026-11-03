import {
  useEffect,
  useRef,
  useImperativeHandle,
  forwardRef,
  useState,
  useCallback,
} from 'react'
import { useTranslation } from 'react-i18next'
import {
  useLiveAudio,
  type LivePlayerAudioStatus,
} from '../hooks/use-live-audio'
import { useWebCodecsRenderLoop } from '../hooks/use-webcodecs-render-loop'
import { AIResultService } from '../services/ai-result-service'
import { drawFaceOverlay } from '../services/face-overlay-renderer'
import { drawPersonOverlay } from '../services/person-overlay-renderer'
import { drawVehicleOverlay } from '../services/vehicle-overlay-renderer'
import {
  isWebCodecsSupported,
  getWebCodecsSupportIssue,
} from '../services/webcodecs-decoder'
import {
  type FaceSubItem,
  type PersonItem,
  type StreamType,
  type VehicleItem,
  type ParsedPacket,
  type StreamStats,
} from '../types'

/** 音频状态类型转发（实现在 `use-live-audio`，组件只做展示）。 */
export type { LivePlayerAudioStatus }

export interface LivePlayerRef {
  decode: (packet: ParsedPacket) => void
  screenshot: (filename?: string) => void
  setMute: (mute: boolean) => void
  reset: () => void
}

export interface LivePlayerProps {
  isMuted?: boolean
  onStatsChange?: (stats: Partial<StreamStats>) => void
  onError?: (error: string) => void
  className?: string
  cameraId?: string | null
  /** 当前播放的码流（决定音频 WS 的 role）。缺省按主流。 */
  streamType?: StreamType
  showAIOverlay?: boolean
  /**
   * 该路流是否有可播的音频。
   *
   * `false` 时**不建立任何音频连接**（不建 WS、不建 AudioContext）——
   * 这是能力帧说"这路没有声音"时唯一正确的行为。默认 `true` 保持对旧调用的兼容。
   */
  hasAudio?: boolean
  /** 出声音量 0..1（不持久化）。只在解禁时读一次 + 变化时实时下发。 */
  volume?: number
  /** 音频状态变化（供格子的喇叭按钮置灰 / 换文案）。 */
  onAudioStatusChange?: (status: LivePlayerAudioStatus) => void
  /** 首个画面画到 canvas 时回调一次，供上层收起「等待首帧」遮罩。 */
  onFirstFrame?: () => void
}

/**
 * 工业级低延迟 WebCodecs 画布流媒体播放器 (LivePlayer)
 * 基于 WebCodecs GPU 硬件解码 + Canvas 2D / rAF 调度渲染，延迟控制在 150ms 以内。
 */
export const LivePlayer = forwardRef<LivePlayerRef, LivePlayerProps>(
  function LivePlayer(
    {
      isMuted = true,
      onStatsChange,
      onError,
      className = '',
      cameraId,
      streamType = 'main',
      showAIOverlay = true,
      hasAudio = true,
      volume = 1,
      onAudioStatusChange,
      onFirstFrame,
    },
    ref
  ): React.JSX.Element {
    const { t } = useTranslation('live')
    const canvasRef = useRef<HTMLCanvasElement | null>(null)
    const overlayCanvasRef = useRef<HTMLCanvasElement | null>(null)
    const lastResolutionRef = useRef<string | null>(null)
    const [isSupported] = useState(() => isWebCodecsSupported())

    const facesRef = useRef<FaceSubItem[]>([])
    const personsRef = useRef<PersonItem[]>([])
    const vehiclesRef = useRef<VehicleItem[]>([])
    const lastEventTimeRef = useRef<number>(0)
    const fadeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

    // 维持最新的回调引用，避免 effect 闭包过期
    const onStatsChangeRef = useRef(onStatsChange)
    onStatsChangeRef.current = onStatsChange
    const onErrorRef = useRef(onError)
    onErrorRef.current = onError
    const onAudioStatusChangeRef = useRef(onAudioStatusChange)
    onAudioStatusChangeRef.current = onAudioStatusChange

    /**
     * 静音状态：prop 是权威（`useLiveGrid.cells[].isMuted` 是唯一真相源），
     * 命令式 `setMute` 只在同一个值上镜像。
     *
     * prop 变化时在渲染期同步过来（React 官方推荐的「用 prop 调整 state」写法），
     * 这样 `muteAll()` 这种从外部一次性置全静音的路径也能生效 —— 若只保留一个
     * 「override 优先」的内部值，`muteAll` 之后 override 仍为 false，声音会继续响。
     */
    const [muted, setMuted] = useState(isMuted)
    const [lastMutedProp, setLastMutedProp] = useState(isMuted)
    if (lastMutedProp !== isMuted) {
      setLastMutedProp(isMuted)
      setMuted(isMuted)
    }

    /** `useLiveAudio` 的 `flush` 经 ref 暴露，避免进渲染循环的依赖数组。 */
    const flushAudioRef = useRef<() => void>(() => {})

    const redrawOverlay = useCallback(() => {
      if (!showAIOverlay) return
      const overlay = overlayCanvasRef.current
      if (!overlay || overlay.width === 0 || overlay.height === 0) return
      const ctx = overlay.getContext('2d')
      if (!ctx) return

      const isExpired = Date.now() - lastEventTimeRef.current > 350
      const currentPersons = isExpired ? [] : personsRef.current
      const currentFaces = isExpired ? [] : facesRef.current
      const currentVehicles = isExpired ? [] : vehiclesRef.current

      // 绘制顺序即层级：人体框在底层，人脸/车牌框压在它上面。
      // 只有第一个调用负责清屏，否则后画的会抹掉前一个。
      drawPersonOverlay({
        ctx,
        width: overlay.width,
        height: overlay.height,
        persons: currentPersons,
        personLabel: t('personLabel', '人体'),
        clear: true,
      })
      drawFaceOverlay({
        ctx,
        width: overlay.width,
        height: overlay.height,
        faces: currentFaces,
        unrecognizedLabel: t('unrecognizedFace', '未登记'),
        clear: false,
      })
      drawVehicleOverlay({
        ctx,
        width: overlay.width,
        height: overlay.height,
        vehicles: currentVehicles,
        unrecognizedLabel: t('unrecognizedPlate', '未识别车牌'),
        clear: false,
      })
    }, [showAIOverlay, t])

    /**
     * 每帧后处理：上报分辨率变化、同步叠加层画布尺寸并重绘目标框。
     * 叠加层画布尺寸跟随主画布，赋值会清空内容，因此随后必须重绘。
     */
    const handleFrameDrawn = useCallback(
      (frame: VideoFrame, canvas: HTMLCanvasElement) => {
        const overlayCanvas = overlayCanvasRef.current
        if (
          overlayCanvas &&
          (overlayCanvas.width !== canvas.width ||
            overlayCanvas.height !== canvas.height)
        ) {
          overlayCanvas.width = canvas.width
          overlayCanvas.height = canvas.height
        }

        const resStr = `${frame.displayWidth}x${frame.displayHeight}`
        if (lastResolutionRef.current !== resStr) {
          lastResolutionRef.current = resStr
          onStatsChangeRef.current?.({ resolution: resStr })
        }

        redrawOverlay()
      },
      [redrawOverlay]
    )

    const {
      decoderRef,
      reset: resetRenderLoop,
      getPresentationClock,
    } = useWebCodecsRenderLoop({
      enabled: isSupported,
      canvasRef,
      traceId: cameraId ?? 'live-player',
      onFrameDrawn: handleFrameDrawn,
      onDecodeError: (error: unknown) => {
        onErrorRef.current?.(
          error instanceof Error ? error.message : 'WebCodecs 解码异常'
        )
      },
      onFirstFrame,
      // 画面跳了，声音必须跟着重锚：丢掉环形缓冲里按旧时间轴排期的样本。
      // 走 ref 中的 `flush`，不进依赖数组（`onResync` 每秒可能触发多次）。
      onResync: () => {
        flushAudioRef.current()
      },
      // ⚠️ 不要用 showAIOverlay 当 restartKey。
      //
      // restartKey 变化会销毁并重建解码器，而重建后必须等下一个 IDR 才能出画
      // ——「切换叠加层开关」会因此变成一次黑屏等待 IDR，短 GOP 也要卡一两秒。
      // 而 onFrameDrawn 走的是 ref，叠加层开关根本不需要重建解码器。
      // 同理：LivePlayer 首次挂载时若 showAIOverlay 由 undefined 变为 true，
      // 也会触发一次重建，这正是「刚开始播放总卡几帧」的来源之一。
      restartKey: '',
    })

    /**
     * 音频通道生命周期交给 `useLiveAudio`：**只有 `!muted && hasAudio && cameraId`
     * 时才建任何东西**（不点喇叭就不建 WS、不建 AudioContext）。
     */
    const { flush: flushAudio } = useLiveAudio({
      cameraId,
      streamType,
      hasAudio,
      muted,
      volume,
      getClock: getPresentationClock,
      onStatusChange: onAudioStatusChange,
    })

    // 渲染循环的 `onResync` 是创建时代入的闭包：经 ref 拿到最新的 `flush`。
    flushAudioRef.current = flushAudio

    // 订阅 AI 推理结果事件流（零 React 重渲染，直接驱动 Canvas 2D）
    useEffect(() => {
      const overlayCanvas = overlayCanvasRef.current
      if (!showAIOverlay || !cameraId) {
        facesRef.current = []
        personsRef.current = []
        vehiclesRef.current = []
        if (overlayCanvas) {
          const ctx = overlayCanvas.getContext('2d')
          ctx?.clearRect(0, 0, overlayCanvas.width, overlayCanvas.height)
        }
        return
      }

      const service = AIResultService.getInstance()
      const unsubscribe = service.subscribeCamera(cameraId, (event) => {
        const extractedFaces: FaceSubItem[] = []
        if (event.persons) {
          for (const p of event.persons) {
            if (p.faces && p.faces.length > 0) {
              extractedFaces.push(...p.faces)
            }
          }
        }

        const extractedVehicles: VehicleItem[] = []
        if (event.vehicles && event.vehicles.length > 0) {
          extractedVehicles.push(...event.vehicles)
        }

        personsRef.current = event.persons ?? []
        facesRef.current = extractedFaces
        vehiclesRef.current = extractedVehicles
        lastEventTimeRef.current = Date.now()
        redrawOverlay()

        if (fadeTimerRef.current) {
          clearTimeout(fadeTimerRef.current)
        }
        fadeTimerRef.current = setTimeout(() => {
          facesRef.current = []
          personsRef.current = []
          vehiclesRef.current = []
          redrawOverlay()
          fadeTimerRef.current = null
        }, 350)
      })

      return () => {
        if (fadeTimerRef.current) {
          clearTimeout(fadeTimerRef.current)
          fadeTimerRef.current = null
        }
        unsubscribe()
        facesRef.current = []
        personsRef.current = []
        vehiclesRef.current = []
        if (overlayCanvas) {
          const ctx = overlayCanvas.getContext('2d')
          ctx?.clearRect(0, 0, overlayCanvas.width, overlayCanvas.height)
        }
      }
    }, [cameraId, showAIOverlay, redrawOverlay])

    useImperativeHandle(
      ref,
      () => ({
        decode: (packet: ParsedPacket) => {
          if (!decoderRef.current) return
          decoderRef.current.decode(packet)
        },
        screenshot: (filename?: string) => {
          const canvas = canvasRef.current
          const overlayCanvas = overlayCanvasRef.current
          if (!canvas || canvas.width === 0 || canvas.height === 0) return
          try {
            let dataUrl: string
            const hasPerception =
              facesRef.current.length > 0 ||
              vehiclesRef.current.length > 0 ||
              personsRef.current.length > 0
            if (
              showAIOverlay &&
              overlayCanvas &&
              overlayCanvas.width > 0 &&
              hasPerception
            ) {
              const offscreen = document.createElement('canvas')
              offscreen.width = canvas.width
              offscreen.height = canvas.height
              const oCtx = offscreen.getContext('2d')
              if (oCtx) {
                oCtx.drawImage(canvas, 0, 0)
                oCtx.drawImage(overlayCanvas, 0, 0)
                dataUrl = offscreen.toDataURL('image/png')
              } else {
                dataUrl = canvas.toDataURL('image/png')
              }
            } else {
              dataUrl = canvas.toDataURL('image/png')
            }

            const a = document.createElement('a')
            a.href = dataUrl
            a.download = `${filename || `snapshot-${Date.now()}`}.png`
            document.body.appendChild(a)
            a.click()
            document.body.removeChild(a)
          } catch {
            onErrorRef.current?.('画面抓拍截图失败')
          }
        },
        setMute: (next: boolean) => {
          // 保留命令式接口（测试与 mock 在用）：只镜像到内部状态，
          // 真实的建连/断连全部由 `muted` 驱动的那条 effect 决定。
          // 无音频（`hasAudio === false`）时不建任何连接，因此 setMute 就是 no-op。
          if (!hasAudio) return
          setMuted(next)
        },
        reset: () => {
          resetRenderLoop()
          lastResolutionRef.current = null
        },
      }),
      [showAIOverlay, resetRenderLoop, decoderRef, hasAudio]
    )

    if (!isSupported) {
      const supportIssue = getWebCodecsSupportIssue()
      const isInsecure = supportIssue === 'insecure_context'
      const handleSwitchHTTPS = () => {
        if (typeof window !== 'undefined') {
          const httpsPort = window.location.port
            ? `:${window.location.port}`
            : ''
          window.location.href = `https://${window.location.hostname}${httpsPort}${window.location.pathname}${window.location.search}`
        }
      }

      return (
        <div
          className={`flex h-full w-full flex-col items-center justify-center bg-black p-4 text-center text-xs text-muted-foreground ${className}`}
        >
          <p className='font-medium text-amber-400'>
            {isInsecure
              ? t(
                  'frame.insecureContextTitle',
                  'WebCodecs 需要安全上下文 (HTTPS)'
                )
              : t('frame.unsupportedBrowserTitle', 'WebCodecs 硬件加速不可用')}
          </p>
          <p className='mt-1 max-w-md text-[11px] text-muted-foreground/70'>
            {isInsecure
              ? t(
                  'frame.insecureContextDesc',
                  '当前通过局域网 HTTP 访问，浏览器安全策略已禁用 GPU 硬件加速。服务已内置自签名证书，建议切换至 HTTPS 访问开启硬解。'
                )
              : t(
                  'frame.unsupportedBrowserDesc',
                  '请使用 Chromium / Safari 现代浏览器，并在 localhost 或 HTTPS 安全上下文下访问。'
                )}
          </p>
          {isInsecure && (
            <button
              type='button'
              onClick={handleSwitchHTTPS}
              className='mt-3 inline-flex items-center gap-1.5 rounded border border-amber-500/20 bg-amber-500/10 px-3 py-1.5 text-xs font-medium text-amber-400 transition-colors hover:bg-amber-500/20'
            >
              {t('frame.insecureContextAction', '切换至 HTTPS 访问')}
            </button>
          )}
        </div>
      )
    }

    return (
      <div
        className={`relative flex h-full w-full items-center justify-center overflow-hidden bg-black ${className}`}
      >
        <div className='relative flex max-h-full max-w-full items-center justify-center'>
          <canvas
            ref={canvasRef}
            className='pointer-events-none max-h-full max-w-full object-contain'
          />
          {showAIOverlay && (
            <canvas
              ref={overlayCanvasRef}
              className='pointer-events-none absolute inset-0 h-full w-full'
            />
          )}
        </div>
      </div>
    )
  }
)
