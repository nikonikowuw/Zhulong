import { type ReactElement, type ReactNode } from 'react'
import { ZhulongLogo } from '@/assets/zhulong-logo'
import { ThemeSwitch } from '@/components/theme-switch'

interface AuthLayoutProps {
  children: ReactNode
}

export function AuthLayout({ children }: AuthLayoutProps): ReactElement {
  return (
    <div className='relative flex min-h-svh w-full flex-col justify-between overflow-hidden bg-background text-foreground selection:bg-primary/20'>
      {/* 全屏暗夜机房背景底图 */}
      <div
        aria-hidden='true'
        className='pointer-events-none absolute inset-0 -z-30 bg-cover bg-center'
        style={{ backgroundImage: `url('/images/auth/bg-datacenter.webp')` }}
      />
      {/* 渐变遮罩：暗色微透明，让真实机房光影与指示灯自然显露 */}
      <div
        aria-hidden='true'
        className='pointer-events-none absolute inset-0 -z-20 bg-background/70 backdrop-blur-[1.5px]'
      />
      {/* 径向暗角：突出中央卡片 */}
      <div
        aria-hidden='true'
        className='pointer-events-none absolute inset-0 -z-10 [background:radial-gradient(circle_at_center,transparent_20%,rgba(0,0,0,0.6)_100%)]'
      />

      {/* 顶部微工具栏 */}
      <header className='relative z-10 flex w-full items-center justify-between border-b border-border/20 px-6 py-4 backdrop-blur-xs'>
        <div className='flex items-center gap-2.5'>
          <ZhulongLogo className='size-7 text-primary transition-transform hover:scale-105' />
          <div className='flex flex-col'>
            <span className='text-sm font-semibold tracking-wider text-foreground uppercase'>
              烛龙 · Zhulong
            </span>
            <span className='text-[10px] tracking-tight text-muted-foreground'>
              Intelligent NVR & Edge Computing
            </span>
          </div>
        </div>
        <div className='flex items-center gap-2'>
          <ThemeSwitch />
        </div>
      </header>

      {/* 主工作区：居中卡片 */}
      <main className='relative z-10 flex flex-1 items-center justify-center p-4 sm:p-8'>
        <div className='w-full max-w-[400px]'>{children}</div>
      </main>

      {/* 底部边缘节点状态与版权栏 */}
      <footer className='relative z-10 flex flex-col items-center justify-between gap-2 border-t border-border/20 bg-background/50 px-6 py-3.5 text-xs text-muted-foreground backdrop-blur-xs sm:flex-row'>
        <div className='flex items-center gap-2'>
          <span className='relative flex size-2'>
            <span className='absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75'></span>
            <span className='relative inline-flex size-2 rounded-full bg-emerald-500'></span>
          </span>
          <span className='font-mono text-[11px]'>NODE-01: ONLINE</span>
          <span className='text-muted-foreground/40'>|</span>
          <span>Linux ARM64 · NPU 加速活跃</span>
        </div>
        <div className='font-mono text-[11px]'>
          <span>Zhulong Project © 2026</span>
        </div>
      </footer>
    </div>
  )
}
