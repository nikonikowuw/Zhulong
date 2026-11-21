import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { authApi } from '../api/auth-api'
import { healthApi } from '../api/health-api'
import { SignIn } from './index'

// 登录页只依赖路由的两个能力：读 redirect、跳转。这里把它们降级成桩，
// 免得为了一个模式切换断言去搭整棵 router。
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useSearch: () => ({}),
}))

vi.mock('../api/health-api', () => ({
  healthApi: { get: vi.fn() },
}))

function renderSignIn() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <SignIn />
    </QueryClientProvider>
  )
}

describe('SignIn 模式切换', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    vi.mocked(healthApi.get).mockReset()
    vi.mocked(healthApi.get).mockResolvedValue({ state: 'ready' })
  })

  it('设备未初始化时直接给初始化向导，而不是让运维对着登录框猜密码', async () => {
    vi.spyOn(authApi, 'getStatus').mockResolvedValue({ initialized: false })

    const { getByText, container } = await renderSignIn()

    await expect.element(getByText('首次部署')).toBeInTheDocument()
    await expect.element(getByText('初始化管理员')).toBeInTheDocument()
    expect(container.textContent).not.toContain('管理员登录')
  })

  it('已初始化时回到常规登录卡片，且不再出现「首次部署」入口', async () => {
    vi.spyOn(authApi, 'getStatus').mockResolvedValue({ initialized: true })

    const { getByText, container } = await renderSignIn()

    await expect.element(getByText('管理员登录')).toBeInTheDocument()
    expect(container.textContent).not.toContain('首次部署')
  })

  it('状态探测失败时明说「无法确认」并给一次就地重试', async () => {
    vi.spyOn(authApi, 'getStatus').mockRejectedValue(new Error('502'))

    const { getByRole } = await renderSignIn()

    await expect
      .element(getByRole('alert'))
      .toHaveTextContent('无法确认设备初始化状态')
    await expect
      .element(getByRole('button', { name: '重试' }))
      .toBeInTheDocument()
  })
})
