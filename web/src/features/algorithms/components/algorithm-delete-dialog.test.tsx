import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render } from 'vitest-browser-react'
import { type Algorithm } from '../data/schema'
import { AlgorithmDeleteDialog } from './algorithm-delete-dialog'

const mockMutate = vi.fn()

// 影响面数据由各用例按需注入；默认返回「干净算法」。
let mockImpact: unknown = {
  algorithmId: 'test_algo',
  algorithmName: '安全帽检测',
  version: '1.2.0',
  status: 'DISABLED',
  boundTaskCount: 0,
  affectedCameraIds: [],
  recognitionCount: 0,
  alarmCount: 0,
  captureCount: 0,
  requiresStop: false,
}
let mockImpactState: 'pending' | 'error' | 'success' = 'success'

vi.mock('../hooks/use-algorithms', () => ({
  useDeleteAlgorithm: () => ({
    mutate: mockMutate,
    isPending: false,
  }),
  useDeletionImpact: () => ({
    data: mockImpactState === 'success' ? mockImpact : undefined,
    isPending: mockImpactState === 'pending',
    isError: mockImpactState === 'error',
  }),
}))

describe('AlgorithmDeleteDialog', () => {
  const queryClient = new QueryClient()

  const mockAlgo: Algorithm = {
    id: 'test_algo',
    name: '安全帽检测',
    version: '1.2.0',
    taskType: 'detection',
    category: 'safety',
    targetArch: 'x86_64',
    targetDevice: 'cpu',
    computeCost: 10,
    memoryMb: 64,
    description: '',
    manifestJson: '{}',
    configSchemaJson: '{}',
    status: 'DISABLED',
    previousVersion: '',
    createdAt: '2026-10-15T00:00:00Z',
    updatedAt: '2026-10-15T00:00:00Z',
  }

  beforeEach(() => {
    vi.clearAllMocks()
    mockImpactState = 'success'
    mockImpact = {
      algorithmId: 'test_algo',
      algorithmName: '安全帽检测',
      version: '1.2.0',
      status: 'DISABLED',
      boundTaskCount: 0,
      affectedCameraIds: [],
      recognitionCount: 0,
      alarmCount: 0,
      captureCount: 0,
      requiresStop: false,
    }
  })

  it('renders confirmation dialog with algorithm name and version', async () => {
    const { getByRole, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmDeleteDialog
          algorithm={mockAlgo}
          open={true}
          onOpenChange={vi.fn()}
        />
      </QueryClientProvider>
    )

    await expect
      .element(getByRole('heading', { name: /确认卸载该算法包？/i }))
      .toBeInTheDocument()

    await expect
      .element(getByText(/安全帽检测/i))
      .toBeInTheDocument()
  })

  it('reports bound tasks and affected cameras', async () => {
    mockImpact = {
      ...(mockImpact as object),
      boundTaskCount: 2,
      affectedCameraIds: ['cam-gate', 'cam-yard'],
    }

    const { getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmDeleteDialog
          algorithm={mockAlgo}
          open={true}
          onOpenChange={vi.fn()}
        />
      </QueryClientProvider>
    )

    await expect
      .element(getByText(/将解绑 2 路分析任务/i))
      .toBeInTheDocument()
    await expect
      .element(getByText(/cam-gate、cam-yard/))
      .toBeInTheDocument()
  })

  it('states that historical evidence is preserved, not deleted', async () => {
    // 这是整个弹窗最关键的语义：用户不能以为证据会没。
    mockImpact = {
      ...(mockImpact as object),
      recognitionCount: 1247,
      alarmCount: 389,
      captureCount: 2103,
    }

    const { getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmDeleteDialog
          algorithm={mockAlgo}
          open={true}
          onOpenChange={vi.fn()}
        />
      </QueryClientProvider>
    )

    await expect
      .element(getByText(/历史证据不会被删除/i))
      .toBeInTheDocument()
    await expect.element(getByText(/1247 条识别/)).toBeInTheDocument()
    await expect.element(getByText(/389 条告警/)).toBeInTheDocument()
    await expect.element(getByText(/2103 条抓拍/)).toBeInTheDocument()
    // 必须说明失去的是「筛选能力」而不是数据本身
    await expect
      .element(getByText(/无法再按该算法筛选/i))
      .toBeInTheDocument()
  })

  it('omits the evidence block when no records exist', async () => {
    const { container } = await render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmDeleteDialog
          algorithm={mockAlgo}
          open={true}
          onOpenChange={vi.fn()}
        />
      </QueryClientProvider>
    )

    expect(container.textContent).not.toContain('历史证据不会被删除')
  })

  it('warns when the algorithm is running', async () => {
    mockImpact = { ...(mockImpact as object), requiresStop: true }

    const { getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmDeleteDialog
          algorithm={mockAlgo}
          open={true}
          onOpenChange={vi.fn()}
        />
      </QueryClientProvider>
    )

    await expect
      .element(getByText(/卸载会先强制停止其进程/i))
      .toBeInTheDocument()
  })

  it('warns instead of staying silent when the impact cannot be loaded', async () => {
    // 静默失败会让用户以为「没有影响」，这比不显示更危险。
    mockImpactState = 'error'

    const { getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmDeleteDialog
          algorithm={mockAlgo}
          open={true}
          onOpenChange={vi.fn()}
        />
      </QueryClientProvider>
    )

    await expect
      .element(getByText(/无法获取影响面统计/i))
      .toBeInTheDocument()
  })

  it('triggers delete mutation when confirm button is clicked', async () => {
    const { getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmDeleteDialog
          algorithm={mockAlgo}
          open={true}
          onOpenChange={vi.fn()}
        />
      </QueryClientProvider>
    )

    const confirmBtn = getByRole('button', { name: /确认卸载/i })
    await confirmBtn.click()

    expect(mockMutate).toHaveBeenCalledWith('test_algo', expect.any(Object))
  })
})
