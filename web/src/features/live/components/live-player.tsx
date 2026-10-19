import {
  useEffect,
  useRef,
  useImperativeHandle,
  forwardRef,
  useState,
  useCallback,
} from 'react'
import { useWebCodecsRenderLoop } from '../hooks/use-webcodecs-render-loop'
import { AIResultService } from '../services/ai-result-service'
import { drawFaceOverlay } from '../services/face-overlay-renderer'
import { isWebCodecsSupported } from '../services/webcodecs-decoder'
import { type FaceSubItem, type ParsedPacket, type StreamStats } from '../types'

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
  showAIOverlay?: boolean
}

/**
 * 工业级低延迟 WebCodecs 画布流媒体播放器 (LivePlayer)
 * 基于 WebCodecs GPU 硬件解码 + Canvas 2D / rAF 调度渲染，延迟控制在 150ms 以内。
 */
export const LivePlayer = forwardRef<LivePlayerRef, LivePlayerProps>(
  function LivePlayer(
    {
      isMuted: _isMuted = true,
      onStatsChange,
      onError,
      className = '',
      cameraId,
      showAIOverlay = true,
    },
    ref
  ): React.JSX.Element {
    const canvasRef = useRef<HTMLCanvasElement | null>(null)
    const overlayCanvasRef = useRef<HTMLCanvasElement | null>(null)
    const lastResolutionRef = useRef<string | null>(null)
    const [isSupported] = useState(() => isWebCodecsSupported())

    const facesRef = useRef<FaceSubItem[]>([])
    const lastEventTimeRef = useRef<number>(0)
    const fadeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

    // 维持最新的回调引用，避免 effect 闭包过期
    const onStatsChangeRef = useRef(onStatsChange)
    onStatsChangeRef.current = onStatsChange
    const onErrorRef = useRef(onError)
    onErrorRef.current = onError

    const redrawOverlay = useCallback(() => {
      if (!showAIOverlay) return
      const overlay = overlayCanvasRef.current
      if (!overlay || overlay.width === 0 || overlay.height === 0) return
      const ctx = overlay.getContext('2d')
      if (!ctx) return

      const isExpired = Date.now() - lastEventTimeRef.current > 350
      drawFaceOverlay({
        ctx,
        width: overlay.width,
        height: overlay.height,
        faces: isExpired ? [] : facesRef.current,
      })
    }, [showAIOverlay])

    /**
     * 每帧后处理：上报分辨率变化、同步叠加层画布尺寸并重绘人脸框。
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

    const { decoderRef, reset: resetRenderLoop } = useWebCodecsRenderLoop({
      enabled: isSupported,
      canvasRef,
      onFrameDrawn: handleFrameDrawn,
      onDecodeError: (error: unknown) => {
        onErrorRef.current?.(
          error instanceof Error ? error.message : 'WebCodecs 解码异常'
        )
      },
      restartKey: String(showAIOverlay),
    })

    // 订阅 AI 推理结果事件流（零 React 重渲染，直接驱动 Canvas 2D）
    useEffect(() => {
      const overlayCanvas = overlayCanvasRef.current
      if (!showAIOverlay || !cameraId) {
        facesRef.current = []
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

        facesRef.current = extractedFaces
        lastEventTimeRef.current = Date.now()
        redrawOverlay()

        if (fadeTimerRef.current) {
          clearTimeout(fadeTimerRef.current)
        }
        fadeTimerRef.current = setTimeout(() => {
          facesRef.current = []
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
            if (
              showAIOverlay &&
              overlayCanvas &&
              overlayCanvas.width > 0 &&
              facesRef.current.length > 0
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
        setMute: (_mute: boolean) => {
          // 当前 ZLM1 视频流为纯图像流，预留音频轨道支持
        },
        reset: () => {
          resetRenderLoop()
          lastResolutionRef.current = null
        },
      }),
      [showAIOverlay, resetRenderLoop, decoderRef]
    )

    if (!isSupported) {
      return (
        <div
          className={`flex h-full w-full flex-col items-center justify-center bg-black p-4 text-center text-xs text-muted-foreground ${className}`}
        >
          <p className='font-medium text-amber-400'>WebCodecs 硬件加速不可用</p>
          <p className='mt-1 text-[11px] text-muted-foreground/70'>
            请使用 Chromium / Safari 现代浏览器，并在 localhost 或 HTTPS
            安全上下文下访问。
          </p>
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
