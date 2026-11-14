import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { log } from '@/lib/logger'
import { ForbiddenError } from './forbidden'
import { GeneralError } from './general-error'
import { MaintenanceError } from './maintenance-error'
import { NotFoundError } from './not-found-error'
import { UnauthorizedError } from './unauthorized-error'

const callOrder: string[] = []
const navigateMock = vi.fn((opts: { to: string }) => {
  callOrder.push(`navigate:${opts.to}`)
})
const historyGoMock = vi.fn((delta: number) => {
  callOrder.push(`history.go:${delta}`)
})

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => navigateMock,
    useRouter: () => ({
      history: {
        go: historyGoMock,
      },
    }),
  }
})

vi.mock('@/lib/logger', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/logger')>()
  return {
    ...actual,
    log: vi.fn(),
  }
})

describe('Error pages', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    callOrder.length = 0
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it.each([
    { Component: UnauthorizedError, code: '401', title: '未授权访问' },
    { Component: ForbiddenError, code: '403', title: '禁止访问' },
    { Component: NotFoundError, code: '404', title: '页面未找到' },
  ])(
    '渲染 $code 的语义化 h1 标题与导航按钮',
    async ({ Component, code, title }) => {
      vi.spyOn(History.prototype, 'length', 'get').mockReturnValue(2)

      const { getByRole, getByText } = await render(<Component />)

      const heading = getByRole('heading', { level: 1, name: title })
      await expect.element(heading).toBeInTheDocument()
      await expect
        .element(getByText(code))
        .toHaveAttribute('aria-hidden', 'true')

      await userEvent.click(getByRole('button', { name: '返回上一页' }))
      expect(historyGoMock).toHaveBeenCalledWith(-1)

      await userEvent.click(getByRole('button', { name: '返回首页' }))
      expect(navigateMock).toHaveBeenCalledWith({ to: '/' })
    }
  )

  it('当历史栈仅 1 条时，返回上一页直接回退到首页', async () => {
    vi.spyOn(History.prototype, 'length', 'get').mockReturnValue(1)

    const { getByRole } = await render(<NotFoundError />)
    await userEvent.click(getByRole('button', { name: '返回上一页' }))

    expect(historyGoMock).not.toHaveBeenCalled()
    expect(navigateMock).toHaveBeenCalledWith({ to: '/' })
  })

  it('503 维护页不渲染无动作死按钮，提供可执行的重试与返回首页', async () => {
    const onRetry = vi.fn()
    const { getByRole } = await render(<MaintenanceError onRetry={onRetry} />)

    await expect
      .element(getByRole('heading', { level: 1, name: '系统正在维护中' }))
      .toBeInTheDocument()
    await expect
      .element(getByRole('button', { name: '了解更多' }))
      .not.toBeInTheDocument()

    await userEvent.click(getByRole('button', { name: '重试' }))
    expect(onRetry).toHaveBeenCalledTimes(1)

    await userEvent.click(getByRole('button', { name: '返回首页' }))
    expect(navigateMock).toHaveBeenCalledWith({ to: '/' })
  })

  it('GeneralError 上报脱敏日志，并在重试/返回/回首页前调用 reset', async () => {
    vi.spyOn(History.prototype, 'length', 'get').mockReturnValue(2)
    const reset = vi.fn(() => {
      callOrder.push('reset')
    })
    const err = new Error('boom secret=123')

    const { container, getByRole, getByText } = await render(
      <GeneralError error={err} reset={reset} className='custom-err' />
    )

    expect(container.firstElementChild?.className).toContain('custom-err')
    await expect
      .element(getByRole('heading', { level: 1, name: '系统发生异常' }))
      .toBeInTheDocument()

    await expect.element(getByText('[开发模式异常堆栈]')).toBeInTheDocument()
    const traceBox = container.querySelector('[dir="ltr"]')
    expect(traceBox).not.toBeNull()
    expect(traceBox?.className).not.toContain('text-left')
    expect(traceBox?.className).toContain('text-start')
    expect(traceBox?.textContent).toContain('boom secret=123')

    await vi.waitFor(() => {
      expect(log).toHaveBeenCalledWith('error', 'router.error_boundary', {
        boundary: 'route_error',
      })
    })

    await userEvent.click(getByRole('button', { name: '重试' }))
    await userEvent.click(getByRole('button', { name: '返回上一页' }))
    await userEvent.click(getByRole('button', { name: '返回首页' }))

    expect(callOrder).toEqual([
      'reset',
      'reset',
      'history.go:-1',
      'reset',
      'navigate:/',
    ])
  })

  it('GeneralError minimal 模式隐藏状态码与操作按钮，但保留语义化 h1', async () => {
    const { getByRole, getByText } = await render(
      <GeneralError minimal reset={vi.fn()} />
    )

    await expect
      .element(getByRole('heading', { level: 1, name: '系统发生异常' }))
      .toBeInTheDocument()
    await expect.element(getByText('500')).not.toBeInTheDocument()
    await expect
      .element(getByRole('button', { name: '重试' }))
      .not.toBeInTheDocument()
    await expect
      .element(getByRole('button', { name: '返回首页' }))
      .not.toBeInTheDocument()
  })
})
