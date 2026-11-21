import { type ReactElement, useEffect, useRef, useState } from 'react'
import { cn } from '@/lib/utils'
import { mountTerrainScene } from './terrain-scene'

interface TerrainBackdropProps {
  className?: string
  onUnavailable?: () => void
}

/**
 * 全屏 Three.js 流动地形画背景层（仅装饰，不拦截任何鼠标/键盘交互）。
 * WebGL 不可用或上下文丢失时静默卸载画布，把画面交还给底层 CSS 渐变兜底。
 */
export function TerrainBackdrop({
  className,
  onUnavailable,
}: TerrainBackdropProps): ReactElement | null {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const onUnavailableRef = useRef(onUnavailable)
  const [isReady, setIsReady] = useState(false)
  const [isUnavailable, setIsUnavailable] = useState(false)

  useEffect(() => {
    onUnavailableRef.current = onUnavailable
  }, [onUnavailable])

  useEffect(() => {
    if (isUnavailable) {
      return
    }
    const container = containerRef.current
    if (!container) {
      return
    }

    const handle = mountTerrainScene(container, {
      onReady: () => setIsReady(true),
      onUnavailable: () => {
        setIsUnavailable(true)
        onUnavailableRef.current?.()
      },
    })

    return () => {
      handle?.dispose()
    }
  }, [isUnavailable])

  if (isUnavailable) {
    return null
  }

  return (
    <div
      ref={containerRef}
      aria-hidden='true'
      data-testid='auth-terrain-backdrop'
      data-ready={isReady ? 'true' : 'false'}
      className={cn(
        'pointer-events-none absolute inset-0 overflow-hidden transition-opacity duration-700',
        isReady ? 'opacity-100' : 'opacity-0',
        className
      )}
    />
  )
}
