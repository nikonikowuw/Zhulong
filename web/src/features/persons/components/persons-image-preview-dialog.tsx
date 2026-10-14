import { useEffect, useRef, useState } from 'react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import {
  ExternalLink,
  Maximize2,
  Minimize2,
  RotateCcw,
  X,
  ZoomIn,
  ZoomOut,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { usePersonsContext } from './persons-provider'

export function PersonsImagePreviewDialog() {
  const { t } = useTranslation('persons')
  const { previewImage, setPreviewImage } = usePersonsContext()
  const containerRef = useRef<HTMLDivElement>(null)

  const [scale, setScale] = useState(1)
  const [isFullscreen, setIsFullscreen] = useState(false)

  // 当预览图片切换或打开时，重置缩放和全屏状态
  useEffect(() => {
    if (previewImage) {
      setScale(1)
    }
  }, [previewImage])

  // 监听原生全屏状态变化
  useEffect(() => {
    const handleFullscreenChange = () => {
      setIsFullscreen(Boolean(document.fullscreenElement))
    }
    document.addEventListener('fullscreenchange', handleFullscreenChange)
    return () => {
      document.removeEventListener('fullscreenchange', handleFullscreenChange)
    }
  }, [])

  const handleClose = () => {
    if (document.fullscreenElement) {
      document.exitFullscreen().catch(() => {})
    }
    setPreviewImage(null)
  }

  const toggleFullscreen = async () => {
    try {
      if (!document.fullscreenElement) {
        if (containerRef.current) {
          await containerRef.current.requestFullscreen()
        }
      } else {
        await document.exitFullscreen()
      }
    } catch {
      // 容错降级
    }
  }

  const zoomIn = () => setScale((s) => Math.min(3, Math.round((s + 0.25) * 100) / 100))
  const zoomOut = () => setScale((s) => Math.max(0.5, Math.round((s - 0.25) * 100) / 100))
  const resetZoom = () => setScale(1)

  // 鼠标滚轮缩放
  const handleWheel = (e: React.WheelEvent) => {
    e.stopPropagation()
    if (e.deltaY < 0) {
      zoomIn()
    } else {
      zoomOut()
    }
  }

  // 双击在 1x 与 2x 间切换
  const handleDoubleClick = (e: React.MouseEvent) => {
    e.stopPropagation()
    setScale((s) => (s > 1 ? 1 : 2))
  }

  if (!previewImage) return null

  return (
    <DialogPrimitive.Root open={Boolean(previewImage)} onOpenChange={(v) => !v && handleClose()}>
      <DialogPrimitive.Portal>
        {/* 全屏黑色遮罩 */}
        <DialogPrimitive.Overlay className='fixed inset-0 z-50 bg-black/95 backdrop-blur-md transition-opacity data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:animate-in data-[state=open]:fade-in-0' />

        <DialogPrimitive.Content
          ref={containerRef}
          aria-describedby={undefined}
          className='fixed inset-0 z-50 flex h-screen w-screen flex-col justify-between overflow-hidden p-0 text-white select-none focus:outline-hidden'
        >
          {/* 无障碍隐藏标题 */}
          <DialogPrimitive.Title className='sr-only'>
            {previewImage.title || 'Full-Screen Face Preview'}
          </DialogPrimitive.Title>

          {/* 顶部悬浮控制栏 */}
          <div className='z-10 flex items-center justify-between gap-3 bg-gradient-to-b from-black/85 via-black/50 to-transparent p-4 sm:px-6'>
            <div className='flex min-w-0 items-center gap-2'>
              <span className='truncate font-mono text-xs sm:text-sm font-medium text-white/90 drop-shadow-xs'>
                {previewImage.title}
              </span>
              <span className='rounded-full bg-white/10 px-2 py-0.5 text-[10px] text-white/70'>
                {Math.round(scale * 100)}%
              </span>
            </div>

            {/* 操作按钮组 */}
            <div className='flex shrink-0 items-center gap-1 sm:gap-2 rounded-full bg-white/10 p-1 backdrop-blur-md border border-white/15'>
              <Button
                variant='ghost'
                size='icon'
                onClick={zoomOut}
                disabled={scale <= 0.5}
                className='h-8 w-8 text-white hover:bg-white/20 hover:text-white disabled:opacity-40'
                title='缩小 (-)'
              >
                <ZoomOut className='h-4 w-4' />
              </Button>
              <Button
                variant='ghost'
                size='icon'
                onClick={resetZoom}
                className='h-8 w-8 text-white hover:bg-white/20 hover:text-white'
                title='1:1 还原'
              >
                <RotateCcw className='h-3.5 w-3.5' />
              </Button>
              <Button
                variant='ghost'
                size='icon'
                onClick={zoomIn}
                disabled={scale >= 3}
                className='h-8 w-8 text-white hover:bg-white/20 hover:text-white disabled:opacity-40'
                title='放大 (+)'
              >
                <ZoomIn className='h-4 w-4' />
              </Button>

              <div className='h-4 w-px bg-white/20' />

              <Button
                variant='ghost'
                size='icon'
                onClick={toggleFullscreen}
                className='h-8 w-8 text-white hover:bg-white/20 hover:text-white'
                title={isFullscreen ? '退出全屏' : '全屏显示'}
              >
                {isFullscreen ? (
                  <Minimize2 className='h-4 w-4' />
                ) : (
                  <Maximize2 className='h-4 w-4' />
                )}
              </Button>

              <Button
                variant='ghost'
                size='icon'
                asChild
                className='h-8 w-8 text-white hover:bg-white/20 hover:text-white'
                title='在新标签页中查看原图'
              >
                <a href={previewImage.url} target='_blank' rel='noopener noreferrer'>
                  <ExternalLink className='h-4 w-4' />
                </a>
              </Button>

              <div className='h-4 w-px bg-white/20' />

              <DialogPrimitive.Close asChild>
                <Button
                  variant='ghost'
                  size='icon'
                  onClick={handleClose}
                  className='h-8 w-8 text-white hover:bg-destructive hover:text-destructive-foreground'
                  title='关闭 (Esc)'
                >
                  <X className='h-4 w-4' />
                </Button>
              </DialogPrimitive.Close>
            </div>
          </div>

          {/* 中央大图展示区（点击空白区域关闭） */}
          <div
            onClick={handleClose}
            onWheel={handleWheel}
            className='relative flex flex-1 items-center justify-center overflow-hidden p-4 sm:p-8 cursor-zoom-out'
          >
            <img
              src={previewImage.url}
              alt={previewImage.title}
              onDoubleClick={handleDoubleClick}
              onClick={(e) => e.stopPropagation()}
              style={{
                transform: `scale(${scale})`,
                transition: 'transform 0.15s ease-out',
              }}
              className='max-h-[85vh] max-w-[90vw] object-contain rounded-lg shadow-2xl drop-shadow-[0_15px_35px_rgba(0,0,0,0.85)] cursor-default'
            />
          </div>

          {/* 底部操作快捷提示 */}
          <div className='z-10 flex items-center justify-center pb-4 text-center'>
            <p className='rounded-full bg-black/40 px-3 py-1 font-mono text-[11px] text-white/60 backdrop-blur-xs'>
              {t('actions.cancel')} (ESC) · 滚轮缩放 · 双击快速放大/还原
            </p>
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}
