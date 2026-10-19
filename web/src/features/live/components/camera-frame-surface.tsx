import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { Loader2, VideoOff } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useStreamConnection } from '../hooks/use-stream-connection'
import { useWebCodecsRenderLoop } from '../hooks/use-webcodecs-render-loop'
import {
  isSameRect,
  measureContentRect,
  type ImageRect,
} from '../services/frame-geometry'
import { isWebCodecsSupported } from '../services/webcodecs-decoder'
import { type ParsedPacket } from '../types'

export type { ImageRect }

export type FrameSurfaceState = 'no-camera' | 'loading' | 'live' | 'unavailable'

export interface CameraFrameSurfaceProps {
  cameraId: string | null
  /** 冻结画面：停止解码新帧，保留当前画面以便精细绘制。 */
  paused?: boolean
  /**
   * 叠加层。容器已按画面实际绘制区定位（letterbox 留白不计入），
   * 因此回调内可直接使用相对坐标映射，无需自行处理留白。
   */
  overlay?: (imageRect: ImageRect) => ReactNode
  onStateChange?: (state: FrameSurfaceState) => void
  className?: string
}

export function CameraFrameSurface({
  cameraId,
  paused = false,
  overlay,
  onStateChange,
  className = '',
}: CameraFrameSurfaceProps): React.JSX.Element {
  const { t } = useTranslation('live')

  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const pausedRef = useRef(paused)
  const onStateChangeRef = useRef(onStateChange)

  useEffect(() => {
    pausedRef.current = paused
  }, [paused])

  useEffect(() => {
    onStateChangeRef.current = onStateChange
  }, [onStateChange])

  const [renderedCameraId, setRenderedCameraId] = useState<string | null>(null)
  const [imageRect, setImageRect] = useState<ImageRect | null>(null)
  const [isSupported] = useState(() => isWebCodecsSupported())

  const hasRenderedFirstFrame =
    renderedCameraId === cameraId && Boolean(cameraId)

  /**
   * 仅在几何真正变化时提交状态。
   * 每帧无条件 setState 会让整个叠加层（含区域编辑器）按帧率重渲染。
   */
  const commitImageRect = useCallback(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const next = measureContentRect(canvas)
    setImageRect((prev) => (isSameRect(prev, next) ? prev : next))
  }, [])

  const handleFrameDrawn = useCallback(
    (_frame: VideoFrame, _canvas: HTMLCanvasElement) => {
      commitImageRect()
      setRenderedCameraId(cameraId)
    },
    [cameraId, commitImageRect]
  )

  const handleDecodeError = useCallback(() => {
    // 状态经 useStreamConnection 统一暴露
  }, [])

  const { decoderRef } = useWebCodecsRenderLoop({
    enabled: Boolean(cameraId) && isSupported,
    canvasRef,
    isPaused: () => pausedRef.current,
    onFrameDrawn: handleFrameDrawn,
    onDecodeError: handleDecodeError,
    restartKey: cameraId ?? '',
  })

  const handlePacket = useCallback(
    (packet: ParsedPacket) => {
      if (pausedRef.current) return
      decoderRef.current?.decode(packet)
    },
    [decoderRef]
  )

  const { status, errorMessage, retry } = useStreamConnection({
    cameraId,
    streamType: 'main',
    enabled: Boolean(cameraId) && isSupported,
    onPacket: handlePacket,
  })

  let currentState: FrameSurfaceState = 'no-camera'
  if (!cameraId) {
    currentState = 'no-camera'
  } else if (!isSupported || status === 'error' || status === 'closed') {
    currentState = 'unavailable'
  } else if (status === 'connected' && hasRenderedFirstFrame) {
    currentState = 'live'
  } else {
    currentState = 'loading'
  }

  useEffect(() => {
    onStateChangeRef.current?.(currentState)
  }, [currentState])

  // 容器尺寸变化时重算内容区
  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return

    const ro = new ResizeObserver(() => commitImageRect())
    ro.observe(canvas)
    return () => ro.disconnect()
  }, [commitImageRect])

  return (
    <div
      className={`relative h-full w-full overflow-hidden bg-black select-none ${className}`}
    >
      {/* 画布绝对定位撑满容器：作为 flex item 时 min-width:auto 会把它钉在
          内在尺寸（如 1920px）上，w-full 无法收缩，画面会溢出并被 overflow-hidden 裁掉。
          letterbox 交给 object-contain，元素盒因此恒等于容器盒，坐标空间可精确计算。 */}
      <canvas
        ref={canvasRef}
        className='pointer-events-none absolute inset-0 h-full w-full object-contain'
      />

      {/* 叠加层容器精确覆盖画面绘制区，letterbox 留白不计入坐标空间 */}
      {hasRenderedFirstFrame && imageRect && overlay && (
        <div
          className='pointer-events-auto absolute'
          style={{
            left: `${imageRect.offsetX}px`,
            top: `${imageRect.offsetY}px`,
            width: `${imageRect.width}px`,
            height: `${imageRect.height}px`,
          }}
        >
          {overlay(imageRect)}
        </div>
      )}

      {currentState === 'loading' && (
        <div className='absolute inset-0 flex flex-col items-center justify-center gap-2 bg-black/60 text-white/80'>
          <Loader2 className='h-8 w-8 animate-spin text-primary' />
          <span className='text-xs'>
            {!hasRenderedFirstFrame && status === 'connected'
              ? t('frame.waitingFirstFrame')
              : t('frame.connecting')}
          </span>
        </div>
      )}

      {currentState === 'unavailable' && (
        <div className='absolute inset-0 flex flex-col items-center justify-center gap-2 bg-black/80 p-4 text-center text-white/80'>
          <VideoOff className='h-8 w-8 text-destructive' />
          <span className='text-xs'>
            {!isSupported
              ? t('frame.unsupported')
              : errorMessage || t('frame.streamUnavailable')}
          </span>
          {isSupported && (
            <button
              type='button'
              onClick={retry}
              className='mt-2 rounded bg-muted px-2.5 py-1 text-xs text-foreground hover:bg-muted/80'
            >
              {t('frame.retry')}
            </button>
          )}
        </div>
      )}

      {currentState === 'no-camera' && (
        <div className='absolute inset-0 flex flex-col items-center justify-center gap-2 text-muted-foreground'>
          <VideoOff className='h-8 w-8' />
          <span className='text-xs'>{t('frame.noCamera')}</span>
        </div>
      )}
    </div>
  )
}
