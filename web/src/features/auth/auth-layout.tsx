import { lazy, Suspense, type ReactElement, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { ZhulongLogo } from '@/assets/zhulong-logo'
import { statusDotClass, type StatusLevel } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { ThemeSwitch } from '@/components/theme-switch'
import { healthApi, type SystemHealthState } from './api/health-api'

const LazyTerrainBackdrop = lazy(() =>
  import('./components/terrain-backdrop').then((mod) => ({
    default: mod.TerrainBackdrop,
  }))
)

interface AuthLayoutProps {
  children: ReactNode
}

const HEALTH_LEVEL: Record<SystemHealthState, StatusLevel> = {
  ready: 'active',
  degraded: 'warning',
  starting: 'warning',
  unavailable: 'destructive',
}

const HEALTH_LABEL_KEY: Record<SystemHealthState, string> = {
  ready: 'layout.healthReady',
  degraded: 'layout.healthDegraded',
  starting: 'layout.healthStarting',
  unavailable: 'layout.healthUnavailable',
}

export function AuthLayout({ children }: AuthLayoutProps): ReactElement {
  const { t } = useTranslation('auth')
  const { data: health, isPending } = useQuery({
    queryKey: ['system', 'health'],
    queryFn: () => healthApi.get(),
    staleTime: 30_000,
    retry: false,
    refetchInterval: 30_000,
    refetchOnWindowFocus: false,
  })

  const healthState: SystemHealthState | undefined = isPending
    ? undefined
    : (health?.state ?? 'unavailable')
  const level: StatusLevel = healthState
    ? HEALTH_LEVEL[healthState]
    : 'inactive'
  const healthLabel = healthState
    ? t(HEALTH_LABEL_KEY[healthState])
    : t('layout.healthProbing')

  return (
    /* `isolate` 是必需的：没有层叠上下文时，下面三个负 z-index 背景层会被
       提升到 root 层级，压在父容器自己的 bg-background 之下（背景画永远不可见）。 */
    <div className='relative isolate flex min-h-svh w-full flex-col justify-between overflow-hidden bg-background text-foreground selection:bg-primary/20'>
      {/* 静态 CSS 氛围兜底层：chunk 加载前与 WebGL 不可用/上下文丢失时兜底，深浅主题自适应 */}
      <div
        aria-hidden='true'
        data-testid='auth-backdrop-fallback'
        className='pointer-events-none absolute inset-0 -z-30'
      >
        <div
          className='absolute inset-0'
          style={{
            backgroundImage:
              'radial-gradient(ellipse 85% 52% at 50% 78%, color-mix(in oklab, var(--brand-signal) 10%, transparent), transparent 72%), radial-gradient(ellipse 95% 65% at 50% 25%, color-mix(in oklab, var(--primary) 6%, transparent), transparent 76%)',
          }}
        />
        <div
          className='absolute inset-0 opacity-60'
          style={{
            backgroundImage:
              'radial-gradient(color-mix(in oklab, var(--foreground) 12%, transparent) 1px, transparent 1px)',
            backgroundSize: '28px 28px',
            maskImage:
              'radial-gradient(ellipse at center, var(--foreground) 25%, transparent 80%)',
          }}
        />
      </div>

      {/* Three.js 3D 流动地形画层（按需懒加载，仅登录路由消费） */}
      <div
        aria-hidden='true'
        className='pointer-events-none absolute inset-0 -z-20'
      >
        <Suspense fallback={null}>
          <LazyTerrainBackdrop />
        </Suspense>
      </div>

      {/* 柔边径向暗角/留白层：让居中卡片轮廓在深浅双模下都清晰突出 */}
      <div
        aria-hidden='true'
        className='pointer-events-none absolute inset-0 -z-10'
        style={{
          backgroundImage:
            'radial-gradient(circle at center, transparent 36%, color-mix(in oklab, var(--background) 55%, transparent) 100%)',
        }}
      />

      {/* Minimal top toolbar */}
      <header className='relative z-10 flex w-full items-center justify-between border-b border-border/20 bg-background/70 px-6 py-4 backdrop-blur-sm'>
        <div className='flex items-center gap-2.5'>
          {/* 品牌名已经以文本形式朗读，logo 只是装饰，避免读屏重复播报。 */}
          <ZhulongLogo
            aria-hidden='true'
            className='size-7 motion-safe:transition-transform motion-safe:hover:scale-105'
          />
          <div className='flex flex-col'>
            <span className='text-sm font-semibold tracking-wider text-foreground uppercase'>
              {t('layout.brand')}
            </span>
            <span className='text-[11px] tracking-tight text-muted-foreground'>
              {t('layout.tagline')}
            </span>
          </div>
        </div>
        <div className='flex items-center gap-2'>
          <ThemeSwitch />
        </div>
      </header>

      {/* Main work area: centered card */}
      <main className='relative z-10 flex flex-1 items-center justify-center p-4 sm:p-8'>
        <div className='w-full max-w-[400px]'>{children}</div>
      </main>

      {/* Bottom status bar. 状态来自 /health，不再硬编码 "NODE-01: ONLINE"。 */}
      <footer className='relative z-10 flex flex-col items-center justify-between gap-1.5 border-t border-border/20 bg-background/80 px-6 py-3 text-xs text-muted-foreground backdrop-blur-sm sm:flex-row sm:gap-4'>
        <div className='flex min-w-0 items-center gap-2'>
          <span className='relative flex size-2 shrink-0'>
            <span
              className={cn(
                'absolute inline-flex h-full w-full rounded-full opacity-75',
                // 无限循环动画必须尊重 prefers-reduced-motion（WCAG 2.3.3）
                'motion-safe:animate-ping',
                statusDotClass(level)
              )}
            />
            <span
              className={cn(
                'relative inline-flex size-2 rounded-full',
                statusDotClass(level)
              )}
            />
          </span>
          {/* 状态文案不换行：390px 下旧版把 "NODE-01:" 和 "ONLINE" 拆成两行 */}
          <span className='truncate font-mono'>{healthLabel}</span>
          {health?.hardwareDecode !== undefined ? (
            <>
              <span className='hidden text-muted-foreground/40 sm:inline'>
                |
              </span>
              <span className='hidden truncate sm:inline'>
                {health.hardwareDecode
                  ? t('layout.decodeHardware')
                  : t('layout.decodeSoftware')}
              </span>
            </>
          ) : null}
        </div>
        <div className='shrink-0 font-mono'>
          {t('layout.copyright', { year: new Date().getFullYear() })}
        </div>
      </footer>
    </div>
  )
}
