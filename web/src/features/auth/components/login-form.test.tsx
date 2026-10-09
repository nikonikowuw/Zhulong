import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { LoginForm } from './login-form'
import { authApi } from '../api/auth-api'

const setUserMock = vi.fn()

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
  })

  it('渲染用户名、密码输入框与登录按钮', async () => {
    const { getByRole, getByPlaceholder } = await render(<LoginForm />)

    await expect.element(getByPlaceholder('请输入用户名')).toBeInTheDocument()
    await expect.element(getByPlaceholder('请输入密码')).toBeInTheDocument()
    await expect.element(getByRole('button', { name: /^登录$/i })).toBeInTheDocument()
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
})
