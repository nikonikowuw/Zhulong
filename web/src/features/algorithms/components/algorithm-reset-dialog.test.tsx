import { AxiosError } from 'axios'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render } from 'vitest-browser-react'
import { type Algorithm } from '../data/schema'
import { AlgorithmResetDialog } from './algorithm-reset-dialog'

const mockMutate = vi.fn()
const mockToastSuccess = vi.hoisted(() => vi.fn())
const mockToastError = vi.hoisted(() => vi.fn())
const mockCopyToClipboard = vi.hoisted(() => vi.fn())

vi.mock('../hooks/use-algorithms', () => ({
  useResetAlgorithm: () => ({
    mutate: mockMutate,
    isPending: false,
  }),
}))

vi.mock('@/lib/clipboard', () => ({
  copyToClipboard: mockCopyToClipboard,
}))

vi.mock('sonner', () => ({
  toast: {
    success: mockToastSuccess,
    error: mockToastError,
  },
}))

/** 构造一个处于熔断态的算法包。 */
const crashLoopAlgorithm: Algorithm = {
  id: 'helmet_detector',
  name: '安全帽检测',
  version: '1.2.0',
  taskType: 'detection',
  category: 'safety',
  targetArch: 'aarch64',
  targetDevice: 'rknn',
  computeCost: 30,
  memoryMb: 128,
  description: '',
  manifestJson: '{}',
  configSchemaJson: '{}',
  status: 'CRASH_LOOP',
  previousVersion: '',
  createdAt: '2026-10-15T00:00:00Z',
  updatedAt: '2026-10-15T00:00:00Z',
}

function serverError(status: number, message: string): AxiosError {
  const error = new AxiosError(message)
  error.response = {
    status,
    data: { code: 'ALGORITHM_NOT_IN_CRASH_LOOP', message, data: null },
  } as AxiosError['response']
  // api-client 的响应拦截器会把服务端 message 覆写到 error.message。
  error.message = message
  return error
}

describe('AlgorithmResetDialog', () => {
  const queryClient = new QueryClient()

  beforeEach(() => {
    vi.clearAllMocks()
  })

  const renderDialog = (onOpenChange = vi.fn()) =>
    render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmResetDialog
          algorithm={crashLoopAlgorithm}
          open={true}
          onOpenChange={onOpenChange}
        />
      </QueryClientProvider>
    )

  it('renders the crash-loop explanation and root-cause checklist', async () => {
    const { getByRole, getByText } = await renderDialog()

    await expect
      .element(getByRole('heading', { name: /算法已进入熔断状态/i }))
      .toBeInTheDocument()

    // 根因提示必须点名 runner 日志的检索字段，否则用户 reset 完还会再熔断
    await expect
      .element(
        getByText(
          /runner 的 stdout \/ stderr 已转发进宿主日志（字段 algorithm=helmet_detector）/i
        )
      )
      .toBeInTheDocument()
  })

  it('does not claim reset restarts the algorithm', async () => {
    const { getByText } = await renderDialog()

    await expect
      .element(getByText(/不会自动重启算法，也不会改动任务绑定/i))
      .toBeInTheDocument()
  })

  it('triggers the reset mutation for the algorithm id', async () => {
    const { getByRole } = await renderDialog()

    await getByRole('button', { name: /重置熔断/i }).click()

    expect(mockMutate).toHaveBeenCalledWith(
      'helmet_detector',
      expect.any(Object)
    )
  })

  it('keeps the dialog open and shows a retained banner when reset fails', async () => {
    const onOpenChange = vi.fn()
    mockMutate.mockImplementation(
      (_id, options: { onError?: (e: unknown) => void }) => {
        options.onError?.(serverError(409, '算法当前不处于 CRASH_LOOP 状态'))
      }
    )

    const { getByRole } = await renderDialog(onOpenChange)

    await getByRole('button', { name: /重置熔断/i }).click()

    // 模态留存级：失败不能关弹窗，错误落在弹窗内的 Banner 上。
    // 弹窗内有两个 Alert（失败 Banner + 排查清单），失败 Banner 先渲染，
    // 而 role="alert" 不参与 name-from-content，故只能按顺序取首个。
    const failureBanner = getByRole('alert').first()
    await expect.element(failureBanner).toBeInTheDocument()
    expect(failureBanner.element().textContent).toContain('重置失败')
    expect(failureBanner.element().textContent).toContain(
      '算法当前不处于 CRASH_LOOP 状态'
    )

    await expect
      .element(getByRole('heading', { name: /算法已进入熔断状态/i }))
      .toBeInTheDocument()
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
  })

  it('falls back to a localized message when the failure is not an HTTP error', async () => {
    mockMutate.mockImplementation(
      (_id, options: { onError?: (e: unknown) => void }) => {
        options.onError?.(new Error(''))
      }
    )

    const { getByRole, getByText } = await renderDialog()

    await getByRole('button', { name: /重置熔断/i }).click()

    await expect
      .element(getByText(/重置失败，请稍后重试。/i))
      .toBeInTheDocument()
  })

  it('copies the algorithm id through the shared clipboard helper', async () => {
    mockCopyToClipboard.mockResolvedValue(true)

    const { getByRole } = await renderDialog()

    await getByRole('button', { name: /复制算法标识/i }).click()

    await vi.waitFor(() => {
      expect(mockCopyToClipboard).toHaveBeenCalledWith('helmet_detector')
    })
    expect(mockToastSuccess).toHaveBeenCalledWith('算法标识已复制到剪贴板')
  })

  it('reports a copy failure instead of claiming success', async () => {
    // 非安全上下文（明文 HTTP 局域网）下两条复制路径都可能失败
    mockCopyToClipboard.mockResolvedValue(false)

    const { getByRole } = await renderDialog()

    await getByRole('button', { name: /复制算法标识/i }).click()

    await vi.waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith('复制失败，请手动复制')
    })
    expect(mockToastSuccess).not.toHaveBeenCalled()
  })

  it('closes the dialog and toasts on success', async () => {
    const onOpenChange = vi.fn()
    mockMutate.mockImplementation(
      (_id, options: { onSuccess?: () => void }) => {
        options.onSuccess?.()
      }
    )

    const { getByRole } = await renderDialog(onOpenChange)

    await getByRole('button', { name: /重置熔断/i }).click()

    await vi.waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
    expect(mockToastSuccess).toHaveBeenCalledWith(
      '已解除熔断，算法回到「已停用」状态'
    )
  })
})
