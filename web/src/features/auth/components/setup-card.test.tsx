import { AxiosError } from 'axios'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { __testing as loggerTesting } from '@/lib/logger'
import { authApi } from '../api/auth-api'
import { SetupCard } from './setup-card'

const setUserMock = vi.fn()

vi.mock('@/stores/auth-store', () => ({
  useAuthStore: () => ({
    auth: {
      setUser: setUserMock,
    },
  }),
}))

describe('SetupCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    loggerTesting.reset()
    loggerTesting.setProduction(true)
  })

  afterEach(() => {
    loggerTesting.reset()
  })

  it('把「首次部署」提到第一视觉层：强调条 + 徽章 + 动作化标题', async () => {
    const { getByText, container } = await render(<SetupCard />)

    await expect.element(getByText('初始化管理员')).toBeInTheDocument()
    await expect.element(getByText('首次部署')).toBeInTheDocument()
    await expect
      .element(getByText('这台设备还没有管理员账号'))
      .toBeInTheDocument()

    // 顶部强调条是整屏唯一一处高饱和色块；装饰性元素必须对读屏隐藏
    const accent = container.querySelector<HTMLElement>('[aria-hidden="true"]')
    expect(accent).not.toBeNull()
    expect(accent?.className).toContain('h-1')
  })

  it('用户名标签与输入框真正关联：按标签能取到 input', async () => {
    const { getByLabelText } = await render(<SetupCard />)

    const usernameInput = getByLabelText('管理员用户名 (3~32 位)')
    await expect.element(usernameInput).toBeInTheDocument()
    expect(usernameInput.element().tagName).toBe('INPUT')
  })

  it('初始化失败时在表单内显示常驻告警，而不是只闪一条 toast', async () => {
    vi.spyOn(authApi, 'initAdmin').mockRejectedValue(
      Object.assign(new AxiosError('Request failed with status code 502'), {
        response: { status: 502, data: '<html>Bad Gateway</html>' },
      })
    )

    const { getByRole, getByPlaceholder } = await render(<SetupCard />)

    await userEvent.fill(
      getByPlaceholder('请输入用户名（例如：admin）'),
      'admin'
    )
    await userEvent.fill(getByPlaceholder('请输入密码'), 'Admin123456')
    await userEvent.fill(getByPlaceholder('请再次输入密码'), 'Admin123456')
    await userEvent.click(getByRole('button', { name: /激活并进入控制台/i }))

    await expect
      .element(getByRole('alert'))
      .toHaveTextContent('无法连接 Zhulong 服务')
    expect(loggerTesting.pendingEntries()).toHaveLength(1)
    expect(loggerTesting.pendingEntries()[0].msg).toBe('server.error')
  })
})
