import { AxiosError } from 'axios'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { __testing as loggerTesting } from '@/lib/logger'
import { authApi } from '../api/auth-api'
import { LoginForm } from './login-form'

const setUserMock = vi.fn()
const toastErrorMock = vi.hoisted(() => vi.fn())

vi.mock('sonner', () => ({
  toast: {
    error: toastErrorMock,
    success: vi.fn(),
  },
}))

vi.mock('@/stores/auth-store', () => ({
  useAuthStore: () => ({
    auth: {
      setUser: setUserMock,
    },
  }),
}))

describe('LoginForm', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    loggerTesting.reset()
    loggerTesting.setProduction(true)
  })

  afterEach(() => {
    loggerTesting.reset()
  })

  it('渲染用户名、密码输入框与登录按钮', async () => {
    const { getByRole, getByPlaceholder, getByLabelText } = await render(
      <LoginForm />
    )

    await expect.element(getByPlaceholder('请输入用户名')).toBeInTheDocument()
    await expect.element(getByPlaceholder('请输入密码')).toBeInTheDocument()
    await expect.element(getByLabelText('用户名')).toBeInTheDocument()
    await expect
      .element(getByRole('button', { name: /^登录$/i }))
      .toBeInTheDocument()
  })

  it('用户名标签与输入框真正关联：按标签能取到 input，点击标签能聚焦', async () => {
    const { getByLabelText, getByText } = await render(<LoginForm />)

    const usernameInput = getByLabelText('用户名')
    await expect.element(usernameInput).toBeInTheDocument()
    // 可访问名必须来自 <label for>，而不是 placeholder（旧结构把 id 打到了外层 div 上）
    expect(usernameInput.element().tagName).toBe('INPUT')

    await userEvent.click(getByText('用户名'))
    await vi.waitFor(() => {
      expect(document.activeElement).toBe(usernameInput.element())
    })
  })

  it('不再渲染无效的「保持登录状态」控件（后端登录载荷无此字段）', async () => {
    const { container } = await render(<LoginForm />)

    expect(container.querySelector('[role="checkbox"]')).toBeNull()
  })

  it('提交成功时调用 authApi.login 与 onSuccess 回调', async () => {
    const mockUser = {
      id: 1,
      username: 'admin',
      createdAt: '2026-10-09T08:00:00Z',
    }
    vi.spyOn(authApi, 'login').mockResolvedValue(mockUser)
    const onSuccess = vi.fn()

    const { getByRole, getByPlaceholder } = await render(
      <LoginForm onSuccess={onSuccess} />
    )

    await userEvent.fill(getByPlaceholder('请输入用户名'), 'admin')
    await userEvent.fill(getByPlaceholder('请输入密码'), 'Admin123456')
    await userEvent.click(getByRole('button', { name: /^登录$/i }))

    await vi.waitFor(() => {
      expect(authApi.login).toHaveBeenCalledWith({
        username: 'admin',
        password: 'Admin123456',
      })
    })

    expect(setUserMock).toHaveBeenCalledWith(mockUser)
    expect(onSuccess).toHaveBeenCalledWith(mockUser)
  })

  it('后端不可达时在表单内显示常驻告警，并明确提示服务不可用', async () => {
    vi.spyOn(authApi, 'login').mockRejectedValue(
      Object.assign(new AxiosError('Request failed with status code 502'), {
        response: { status: 502, data: '<html>Bad Gateway</html>' },
      })
    )

    const { getByRole, getByPlaceholder } = await render(<LoginForm />)

    await userEvent.fill(getByPlaceholder('请输入用户名'), 'admin')
    await userEvent.fill(getByPlaceholder('请输入密码'), 'Admin123456')
    await userEvent.click(getByRole('button', { name: /^登录$/i }))

    // 502 的响应体是代理返回的 HTML，没有信封 message；这里必须落到
    // 「服务不可达」文案，而不是「请求处理失败」这种会让人怀疑密码的通用话术。
    await expect
      .element(getByRole('alert'))
      .toHaveTextContent('无法连接 Zhulong 服务')
    expect(toastErrorMock).toHaveBeenCalledWith(
      expect.stringContaining('无法连接 Zhulong 服务'),
      {
        id: expect.stringContaining('无法连接 Zhulong 服务'),
      }
    )
    expect(toastErrorMock).toHaveBeenCalledTimes(1)
    expect(loggerTesting.pendingEntries()).toHaveLength(1)
    expect(loggerTesting.pendingEntries()[0].msg).toBe('server.error')
  })

  it('登录凭据错误通过 toast 展示服务端本地化文案', async () => {
    const message = '用户名或密码错误'
    vi.spyOn(authApi, 'login').mockRejectedValue(
      Object.assign(new AxiosError('Request failed with status code 401'), {
        response: {
          status: 401,
          data: { code: 'INVALID_CREDENTIALS', message },
        },
      })
    )

    const { getByRole, getByPlaceholder } = await render(<LoginForm />)

    await userEvent.fill(getByPlaceholder('请输入用户名'), 'admin')
    await userEvent.fill(getByPlaceholder('请输入密码'), 'wrong-password')
    await userEvent.click(getByRole('button', { name: /^登录$/i }))

    await expect.element(getByRole('alert')).toHaveTextContent(message)
    expect(toastErrorMock).toHaveBeenCalledWith(message, { id: message })
    expect(toastErrorMock).toHaveBeenCalledTimes(1)
    expect(loggerTesting.pendingEntries()).toHaveLength(1)
  })
})
