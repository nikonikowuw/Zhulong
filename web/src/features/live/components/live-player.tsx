import {
  useEffect,
  useRef,
  useImperativeHandle,
  forwardRef,
  useState,
} from 'react'
import {
  isWebCodecsSupported,
  StreamDecoder,
} from '../services/webcodecs-decoder'
import { type ParsedPacket, type StreamStats } from '../types'

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
}

/**
 * 工业级低延迟 WebCodecs 画布流媒体播放器 (LivePlayer)
 * 基于 WebCodecs GPU 硬件解码 + Canvas 2D / rAF 调度渲染，延迟控制在 150ms 以内。
 */
export const LivePlayer = forwardRef<LivePlayerRef, LivePlayerProps>(
  function LivePlayer(
    { isMuted: _isMuted = true, onStatsChange, onError, className = '' },
    ref
  ): React.JSX.Element {
    const canvasRef = useRef<HTMLCanvasElement | null>(null)
    const pendingFrameRef = useRef<VideoFrame | null>(null)
    const rafIdRef = useRef<number | null>(null)
    const decoderRef = useRef<StreamDecoder | null>(null)
    const lastResolutionRef = useRef<string | null>(null)
    const [isSupported] = useState(() => isWebCodecsSupported())

    // 维持最新的回调引用，避免 effect 闭包过期
    const onStatsChangeRef = useRef(onStatsChange)
    onStatsChangeRef.current = onStatsChange
    const onErrorRef = useRef(onError)
    onErrorRef.current = onError

    useImperativeHandle(
      ref,
      () => ({
        decode: (packet: ParsedPacket) => {
          if (!decoderRef.current) return
          decoderRef.current.decode(packet)
        },
        screenshot: (filename?: string) => {
          const canvas = canvasRef.current
          if (!canvas || canvas.width === 0 || canvas.height === 0) return
          try {
            const dataUrl = canvas.toDataURL('image/png')
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
          if (rafIdRef.current !== null) {
            cancelAnimationFrame(rafIdRef.current)
            rafIdRef.current = null
          }
          if (pendingFrameRef.current) {
            pendingFrameRef.current.close()
            pendingFrameRef.current = null
          }
          decoderRef.current?.reset()
          lastResolutionRef.current = null
        },
      }),
      []
    )

    useEffect(() => {
      if (!isSupported) {
        return
      }

      lastResolutionRef.current = null

      const renderLoop = () => {
        const frame = pendingFrameRef.current
        pendingFrameRef.current = null
        rafIdRef.current = null

        if (frame && canvasRef.current) {
          const canvas = canvasRef.current
          if (
            canvas.width !== frame.displayWidth ||
            canvas.height !== frame.displayHeight
          ) {
            canvas.width = frame.displayWidth
            canvas.height = frame.displayHeight
            const resStr = `${frame.displayWidth}x${frame.displayHeight}`
            if (lastResolutionRef.current !== resStr) {
              lastResolutionRef.current = resStr
              onStatsChangeRef.current?.({ resolution: resStr })
            }
          }

          const ctx = canvas.getContext('2d')
          if (ctx) {
            ctx.drawImage(frame, 0, 0, canvas.width, canvas.height)
          }
          frame.close()
        }
      }

      // 初始化 WebCodecs 流解码器
      const decoder = new StreamDecoder({
        onFrame: (frame: VideoFrame) => {
          if (pendingFrameRef.current) {
            pendingFrameRef.current.close()
          }
          pendingFrameRef.current = frame
          if (rafIdRef.current === null) {
            rafIdRef.current = requestAnimationFrame(renderLoop)
          }
        },
        onError: (err) => {
          onErrorRef.current?.(
            err instanceof Error ? err.message : 'WebCodecs 解码异常'
          )
        },
      })
      decoderRef.current = decoder

      return () => {
        if (rafIdRef.current !== null) {
          cancelAnimationFrame(rafIdRef.current)
          rafIdRef.current = null
        }
        if (pendingFrameRef.current) {
          pendingFrameRef.current.close()
          pendingFrameRef.current = null
        }
        decoder.close()
        decoderRef.current = null
      }
    }, [isSupported])

    if (!isSupported) {
      return (
        <div
          className={`flex h-full w-full flex-col items-center justify-center bg-black p-4 text-center text-xs text-muted-foreground ${className}`}
        >
          <p className='font-medium text-amber-400'>WebCodecs 硬件加速不可用</p>
          <p className='mt-1 text-[11px] text-zinc-500'>
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
        <canvas
          ref={canvasRef}
          className='pointer-events-none max-h-full max-w-full object-contain'
        />
      </div>
    )
  }
)
