import { useEffect, useRef, useImperativeHandle, forwardRef } from 'react'
import {
  loadJessibuca,
  type JessibucaInstance,
} from '../services/jessibuca-loader'
import { type StreamStats } from '../types'

export interface JessibucaPlayerRef {
  screenshot: (filename?: string) => void
  setMute: (mute: boolean) => void
}

interface JessibucaPlayerProps {
  url: string
  isMuted?: boolean
  onStatsChange?: (stats: Partial<StreamStats>) => void
  onError?: (error: string) => void
  className?: string
}

export const JessibucaPlayer = forwardRef<
  JessibucaPlayerRef,
  JessibucaPlayerProps
>(function JessibucaPlayer(
  { url, isMuted = true, onStatsChange, onError, className = '' },
  ref
): React.JSX.Element {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const playerRef = useRef<JessibucaInstance | null>(null)

  useImperativeHandle(
    ref,
    () => ({
      screenshot: (filename?: string) => {
        if (playerRef.current) {
          playerRef.current.screenshot(
            filename || `snapshot-${Date.now()}`,
            'png',
            1.0
          )
        }
      },
      setMute: (mute: boolean) => {
        if (playerRef.current) {
          if (mute) {
            playerRef.current.mute()
          } else {
            playerRef.current.cancelMute()
          }
        }
      },
    }),
    []
  )

  useEffect(() => {
    let isDisposed = false
    let player: JessibucaInstance | null = null

    async function initPlayer() {
      if (!containerRef.current || !url) return

      try {
        const JessibucaClass = await loadJessibuca()
        if (isDisposed || !containerRef.current) return

        player = new JessibucaClass({
          container: containerRef.current,
          decoder: '/vendor/jessibuca/decoder.js',
          videoBuffer: 0.2, // 200ms 极低延迟缓冲
          isResize: false,
          useWCS: true, // WebCodecs 硬解优先
          useMSE: true, // MSE 硬解次选
          autoWasm: true, // WASM SIMD 软解兜底
          demuxUseWorker: true,
          forceNoOffscreen: false,
          isNotMute: !isMuted,
          operateBtns: {
            fullscreen: false,
            screenshot: false,
            play: false,
            audio: false,
            record: false,
          },
          timeout: 10,
        })

        playerRef.current = player

        player.on('videoInfo', (...args: unknown[]) => {
          const info = args[0] as
            | { width?: number; height?: number }
            | undefined
          if (info && info.width && info.height) {
            onStatsChange?.({
              resolution: `${info.width}x${info.height}`,
            })
          }
        })

        player.on('stats', (...args: unknown[]) => {
          const stats = args[0] as
            | { fps?: number; bitrate?: number }
            | undefined
          if (stats) {
            onStatsChange?.({
              fps: Math.round(stats.fps || 0),
              bitrateKbps: Math.round((stats.bitrate || 0) / 1024),
            })
          }
        })

        player.on('error', (...args: unknown[]) => {
          const err = args[0] as unknown
          onError?.(typeof err === 'string' ? err : '播放器发生内部错误')
        })

        await player.play(url)
      } catch (err) {
        if (!isDisposed) {
          onError?.(err instanceof Error ? err.message : '播放器初始化加载失败')
        }
      }
    }

    void initPlayer()

    return () => {
      isDisposed = true
      if (player) {
        void player.destroy()
        playerRef.current = null
      }
    }
  }, [url, isMuted, onError, onStatsChange])

  // 监听容器大小变化重算播放器视口
  useEffect(() => {
    if (!containerRef.current) return

    const observer = new ResizeObserver(() => {
      if (playerRef.current) {
        playerRef.current.resize()
      }
    })

    observer.observe(containerRef.current)
    return () => observer.disconnect()
  }, [])

  return (
    <div
      ref={containerRef}
      className={`relative h-full w-full overflow-hidden bg-black ${className}`}
    />
  )
})
