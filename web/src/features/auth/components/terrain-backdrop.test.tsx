import { afterEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { TerrainBackdrop } from './terrain-backdrop'

function createMockMediaQuery(matches: boolean): MediaQueryList {
  return {
    matches,
    media: '(prefers-reduced-motion: reduce)',
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(() => false),
  }
}

describe('TerrainBackdrop', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('在支持 WebGL 的浏览器中渲染隐藏读屏的 3D 画布，并在卸载时完整销毁', async () => {
    const { container, unmount } = await render(
      <div className='relative h-[480px] w-[800px]'>
        <TerrainBackdrop />
      </div>
    )

    const host = container.querySelector<HTMLElement>(
      '[data-testid="auth-terrain-backdrop"]'
    )
    expect(host).not.toBeNull()
    expect(host?.getAttribute('aria-hidden')).toBe('true')
    expect(host?.classList.contains('pointer-events-none')).toBe(true)

    await vi.waitFor(() => {
      expect(host?.dataset.ready).toBe('true')
    })

    const canvas = host?.querySelector('canvas')
    expect(canvas).not.toBeNull()
    expect(canvas?.getAttribute('aria-hidden')).toBe('true')
    expect(canvas?.classList.contains('pointer-events-none')).toBe(true)

    unmount()
    expect(container.querySelector('canvas')).toBeNull()
  })

  it('尊重 prefers-reduced-motion：只渲染静态一帧而不启动连续动画循环', async () => {
    const originalMatchMedia = window.matchMedia.bind(window)
    vi.spyOn(window, 'matchMedia').mockImplementation((query: string) => {
      if (query === '(prefers-reduced-motion: reduce)') {
        return createMockMediaQuery(true)
      }
      return originalMatchMedia(query)
    })

    const rafSpy = vi.spyOn(window, 'requestAnimationFrame')

    const { container } = await render(
      <div className='relative h-[480px] w-[800px]'>
        <TerrainBackdrop />
      </div>
    )

    const host = container.querySelector<HTMLElement>(
      '[data-testid="auth-terrain-backdrop"]'
    )
    await vi.waitFor(() => {
      expect(host?.dataset.ready).toBe('true')
    })

    expect(rafSpy).not.toHaveBeenCalled()
  })

  it('发生 webglcontextlost 时阻止默认行为、卸载画布并触发降级回调', async () => {
    const onUnavailable = vi.fn()
    const { container } = await render(
      <div className='relative h-[480px] w-[800px]'>
        <TerrainBackdrop onUnavailable={onUnavailable} />
      </div>
    )

    const host = container.querySelector<HTMLElement>(
      '[data-testid="auth-terrain-backdrop"]'
    )
    await vi.waitFor(() => {
      expect(host?.dataset.ready).toBe('true')
    })

    const canvas = host?.querySelector('canvas')
    expect(canvas).not.toBeNull()

    const lostEvent = new Event('webglcontextlost', { cancelable: true })
    canvas?.dispatchEvent(lostEvent)

    expect(lostEvent.defaultPrevented).toBe(true)
    await vi.waitFor(() => {
      expect(onUnavailable).toHaveBeenCalledTimes(1)
      expect(
        container.querySelector('[data-testid="auth-terrain-backdrop"]')
      ).toBeNull()
    })
  })

  it('WebGL 上下文不可用时静默降级，不抛异常且移除画布容器', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null)
    const onUnavailable = vi.fn()

    const { container } = await render(
      <div className='relative h-[480px] w-[800px]'>
        <TerrainBackdrop onUnavailable={onUnavailable} />
      </div>
    )

    await vi.waitFor(() => {
      expect(onUnavailable).toHaveBeenCalledTimes(1)
      expect(
        container.querySelector('[data-testid="auth-terrain-backdrop"]')
      ).toBeNull()
    })
  })
})
