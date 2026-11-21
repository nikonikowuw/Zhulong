import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { healthApi } from './api/health-api'
import { AuthLayout } from './auth-layout'

vi.mock('./api/health-api', () => ({
  healthApi: { get: vi.fn() },
}))

function renderLayout() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <AuthLayout>
        <div>card</div>
      </AuthLayout>
    </QueryClientProvider>
  )
}

describe('AuthLayout', () => {
  beforeEach(() => {
    vi.mocked(healthApi.get).mockReset()
  })

  it('页脚显示后端上报的真实就绪状态，而不是硬编码的 ONLINE', async () => {
    vi.mocked(healthApi.get).mockResolvedValue({
      state: 'ready',
      hardwareDecode: true,
    })

    const { getByText, container } = await renderLayout()

    await expect.element(getByText('服务就绪')).toBeInTheDocument()
    await expect.element(getByText('硬件解码')).toBeInTheDocument()
    expect(container.textContent).not.toContain('NODE-01')
  })

  it('硬件解码不可用时不说「加速活跃」，改报软件解码', async () => {
    vi.mocked(healthApi.get).mockResolvedValue({
      state: 'degraded',
      hardwareDecode: false,
    })

    const { getByText, container } = await renderLayout()

    await expect.element(getByText('服务降级')).toBeInTheDocument()
    await expect.element(getByText('软件解码')).toBeInTheDocument()
    expect(container.textContent).not.toContain('加速')
  })

  it('后端不可达时页脚显示「无法连接服务」', async () => {
    vi.mocked(healthApi.get).mockResolvedValue({ state: 'unavailable' })

    const { getByText } = await renderLayout()

    await expect.element(getByText('无法连接服务')).toBeInTheDocument()
  })

  it('根容器建立层叠上下文，并提供深浅双模兼容的 CSS 兜底层与 Three.js 动态地形画层', async () => {
    vi.mocked(healthApi.get).mockResolvedValue({ state: 'ready' })

    const { container } = await renderLayout()

    const root = container.firstElementChild as HTMLElement
    // 没有 isolate 时，-z-* 背景层会被提升到 root 负层级，压在 bg-background 之下
    expect(getComputedStyle(root).isolation).toBe('isolate')

    const fallback = root.querySelector<HTMLElement>(
      '[data-testid="auth-backdrop-fallback"]'
    )
    expect(fallback).not.toBeNull()
    expect(fallback?.getAttribute('aria-hidden')).toBe('true')
    expect(getComputedStyle(fallback!).display).not.toBe('none')

    await vi.waitFor(() => {
      const terrain = root.querySelector<HTMLElement>(
        '[data-testid="auth-terrain-backdrop"]'
      )
      expect(terrain).not.toBeNull()
      expect(terrain?.dataset.ready).toBe('true')
    })
  })
})
