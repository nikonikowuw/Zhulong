import { useEffect, useRef, useImperativeHandle, forwardRef } from 'react'

export interface MockStreamCanvasRef {
  screenshot: (filename?: string) => void
}

interface MockStreamCanvasProps {
  cameraName?: string
  streamType?: 'main' | 'sub'
  className?: string
}

function formatTimestamp(d: Date): string {
  const pad = (n: number, w = 2) => String(n).padStart(w, '0')
  const datePart = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
  const timePart = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`
  return `${datePart} ${timePart}`
}

// SMPTE 75% 彩条标准色
const SMPTE_COLORS = [
  '#bfbfbf', // 75% 灰白
  '#bfbf00', // 黄
  '#00bfbf', // 青
  '#00bf00', // 绿
  '#bf00bf', // 品红
  '#bf0000', // 红
  '#0000bf', // 蓝
  '#101010', // 超黑
]

export const MockStreamCanvas = forwardRef<
  MockStreamCanvasRef,
  MockStreamCanvasProps
>(function MockStreamCanvas(
  { cameraName = 'Live Camera', streamType = 'main', className = '' },
  ref
): React.JSX.Element {
  const canvasRef = useRef<HTMLCanvasElement | null>(null)

  useImperativeHandle(
    ref,
    () => ({
      screenshot: (filename?: string) => {
        const canvas = canvasRef.current
        if (!canvas) return
        const link = document.createElement('a')
        link.download = `${filename || 'mock-snapshot'}-${Date.now()}.png`
        link.href = canvas.toDataURL('image/png')
        link.click()
      },
    }),
    []
  )

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return

    const ctx = canvas.getContext('2d')
    if (!ctx) return

    let animationFrameId: number
    let scanLineY = 0

    const render = () => {
      const width = canvas.width
      const height = canvas.height

      if (width === 0 || height === 0) {
        animationFrameId = requestAnimationFrame(render)
        return
      }

      // 1. 绘制 SMPTE 彩条背景
      const barWidth = width / SMPTE_COLORS.length
      SMPTE_COLORS.forEach((color, idx) => {
        ctx.fillStyle = color
        ctx.fillRect(idx * barWidth, 0, barWidth, height * 0.75)
      })

      // 底部黑度与色阶区
      ctx.fillStyle = '#0a0a0a'
      ctx.fillRect(0, height * 0.75, width, height * 0.25)

      // 底部渐变阶梯
      const steps = 8
      const stepWidth = width / steps
      for (let i = 0; i < steps; i++) {
        const grayVal = Math.round((i / (steps - 1)) * 255)
        ctx.fillStyle = `rgb(${grayVal},${grayVal},${grayVal})`
        ctx.fillRect(i * stepWidth, height * 0.75, stepWidth, height * 0.12)
      }

      // 2. 动态扫描线 (模拟雷达与视频同步)
      scanLineY = (scanLineY + 2) % height
      ctx.fillStyle = 'rgba(255, 255, 255, 0.12)'
      ctx.fillRect(0, scanLineY, width, 2)

      // 3. 动态时间戳与 OSD 文本
      const timeStr = formatTimestamp(new Date())

      ctx.fillStyle = 'rgba(0, 0, 0, 0.65)'
      ctx.fillRect(16, 16, 360, 48)
      ctx.strokeStyle = 'rgba(255, 255, 255, 0.2)'
      ctx.strokeRect(16, 16, 360, 48)

      ctx.font = 'bold 15px monospace'
      ctx.fillStyle = '#22c55e'
      ctx.fillText(`● LIVE [${streamType.toUpperCase()}]`, 28, 38)

      ctx.font = '13px monospace'
      ctx.fillStyle = '#ffffff'
      ctx.fillText(timeStr, 28, 54)

      // 4. 水印标题
      ctx.fillStyle = 'rgba(0, 0, 0, 0.6)'
      ctx.fillRect(width / 2 - 140, height / 2 - 20, 280, 40)
      ctx.font = 'bold 14px sans-serif'
      ctx.fillStyle = '#f8fafc'
      ctx.textAlign = 'center'
      ctx.fillText(`${cameraName} (TEST STREAM)`, width / 2, height / 2 + 5)
      ctx.textAlign = 'start'

      animationFrameId = requestAnimationFrame(render)
    }

    render()

    return () => {
      cancelAnimationFrame(animationFrameId)
    }
  }, [cameraName, streamType])

  return (
    <canvas
      ref={canvasRef}
      width={1280}
      height={720}
      className={`h-full w-full bg-black object-contain ${className}`}
    />
  )
})
