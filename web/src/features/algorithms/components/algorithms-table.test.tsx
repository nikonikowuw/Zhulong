import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  expectClassesApplied,
  expectDiffersInDarkMode,
  expectPaletteRendersAs,
} from '@/test-utils/colors'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render } from 'vitest-browser-react'
import { CATEGORY_BADGE_CLASS, type CategoryHue } from '@/lib/category-palette'
import { type Algorithm, type AlgorithmTaskType } from '../data/schema'
import { AlgorithmsTable } from './algorithms-table'

let mockAlgorithmsData: Algorithm[] = []
let mockIsLoading = false
const mockEnableMutate = vi.fn()
const mockDisableMutate = vi.fn()

vi.mock('../hooks/use-algorithms', () => ({
  useAlgorithms: () => ({
    data: mockAlgorithmsData,
    isLoading: mockIsLoading,
    error: null,
    refetch: vi.fn(),
  }),
  useEnableAlgorithm: () => ({
    mutate: mockEnableMutate,
    isPending: false,
  }),
  useDisableAlgorithm: () => ({
    mutate: mockDisableMutate,
    isPending: false,
  }),
  useDeleteAlgorithm: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useResetAlgorithm: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  // 表格里会渲染卸载确认弹窗，它依赖影响面查询；这里给一个「干净算法」的
  // 空影响面，避免 hook 未被 mock 导致模块加载失败。
  useDeletionImpact: () => ({
    data: {
      algorithmId: 'face_recognition',
      algorithmName: '人脸识别算法',
      version: '1.0.0',
      status: 'DISABLED',
      boundTaskCount: 0,
      affectedCameraIds: [],
      recognitionCount: 0,
      alarmCount: 0,
      captureCount: 0,
      requiresStop: false,
    },
    isPending: false,
    isError: false,
  }),
}))

describe('AlgorithmsTable', () => {
  const queryClient = new QueryClient()

  const makeAlgorithm = (overrides: Partial<Algorithm> = {}): Algorithm => ({
    id: 'face_recognition',
    name: '人脸识别算法',
    version: '1.0.0',
    taskType: 'person_structure',
    category: 'security',
    targetArch: 'x86_64',
    targetDevice: 'cpu',
    computeCost: 20,
    memoryMb: 128,
    description: '高精度人脸检测与比对',
    manifestJson: '{}',
    configSchemaJson: '{}',
    status: 'DISABLED',
    previousVersion: '',
    createdAt: '2026-10-15T00:00:00Z',
    updatedAt: '2026-10-15T00:00:00Z',
    ...overrides,
  })

  beforeEach(() => {
    vi.clearAllMocks()
    mockAlgorithmsData = []
    mockIsLoading = false
  })

  it('renders empty state when no algorithms exist', async () => {
    mockAlgorithmsData = []
    const { getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmsTable onOpenUpload={vi.fn()} />
      </QueryClientProvider>
    )

    await expect.element(getByText(/尚未安装任何算法包/i)).toBeInTheDocument()
  })

  it('renders table rows when algorithms exist', async () => {
    mockAlgorithmsData = [
      {
        id: 'face_recognition',
        name: '人脸识别算法',
        version: '1.0.0',
        taskType: 'person_structure',
        category: 'security',
        targetArch: 'x86_64',
        targetDevice: 'cpu',
        computeCost: 20,
        memoryMb: 128,
        description: '高精度人脸检测与比对',
        manifestJson: '{}',
        configSchemaJson: '{}',
        status: 'ACTIVE',
        previousVersion: '',
        createdAt: '2026-10-15T00:00:00Z',
        updatedAt: '2026-10-15T00:00:00Z',
      },
    ]

    const { getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmsTable onOpenUpload={vi.fn()} />
      </QueryClientProvider>
    )

    await expect.element(getByText('人脸识别算法')).toBeInTheDocument()

    await expect.element(getByText('v1.0.0')).toBeInTheDocument()

    await expect.element(getByText('face_recognition')).toBeInTheDocument()
  })

  it('enables a disabled algorithm when the scheduling switch is turned on', async () => {
    mockAlgorithmsData = [makeAlgorithm({ id: 'algo_off', status: 'DISABLED' })]

    const { getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmsTable onOpenUpload={vi.fn()} />
      </QueryClientProvider>
    )

    const toggle = getByRole('switch')
    await expect.element(toggle).toBeInTheDocument()
    await toggle.click()

    expect(mockEnableMutate).toHaveBeenCalledWith('algo_off')
    expect(mockDisableMutate).not.toHaveBeenCalled()
  })

  it('disables an active algorithm when the scheduling switch is turned off', async () => {
    mockAlgorithmsData = [makeAlgorithm({ id: 'algo_on', status: 'ACTIVE' })]

    const { getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmsTable onOpenUpload={vi.fn()} />
      </QueryClientProvider>
    )

    const toggle = getByRole('switch')
    await expect.element(toggle).toBeInTheDocument()
    await toggle.click()

    expect(mockDisableMutate).toHaveBeenCalledWith('algo_on')
    expect(mockEnableMutate).not.toHaveBeenCalled()
  })

  it('offers the reset-breaker action only for a crash-loop algorithm', async () => {
    mockAlgorithmsData = [makeAlgorithm({ status: 'ACTIVE' })]

    const active = await render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmsTable onOpenUpload={vi.fn()} />
      </QueryClientProvider>
    )
    expect(active.container.textContent).not.toContain('重置熔断')

    mockAlgorithmsData = [makeAlgorithm({ status: 'CRASH_LOOP' })]

    const crashed = await render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmsTable onOpenUpload={vi.fn()} />
      </QueryClientProvider>
    )
    await expect
      .element(crashed.getByRole('button', { name: /重置熔断/i }))
      .toBeInTheDocument()
  })

  it('disables the scheduling switch for a crash-loop algorithm', async () => {
    mockAlgorithmsData = [
      makeAlgorithm({ id: 'algo_stuck', status: 'CRASH_LOOP' }),
    ]

    const { getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmsTable onOpenUpload={vi.fn()} />
      </QueryClientProvider>
    )

    // 熔断态下启用必然 409 ALGORITHM_IN_CRASH_LOOP，开关必须锁死，
    // 并用 title 说明出路在「重置熔断」而非反复点击开关。
    const toggle = getByRole('switch')
    await expect.element(toggle).toBeDisabled()
    expect(toggle.element().getAttribute('title')).toContain('熔断')
  })

  /**
   * §1.3.6 分类色板槽位分配（期望值，非从组件反推）：
   * 任务类型 → 分类色相槽位。这是**规范约束**，因此在此显式列出作为期望。
   */
  const TASK_TYPE_HUE: Array<[AlgorithmTaskType, string, CategoryHue]> = [
    ['detection', '目标检测', 'blue'],
    ['classification', '图像分类', 'violet'],
    ['person_structure', '结构化人脸', 'emerald'],
  ]

  it.each(TASK_TYPE_HUE)(
    'renders the %s task-type badge on the %s §1.3.6 slot',
    async (taskType, label, hue) => {
      mockAlgorithmsData = [makeAlgorithm({ taskType, name: `${label}算法` })]

      const { getByRole } = await render(
        <QueryClientProvider client={queryClient}>
          <AlgorithmsTable onOpenUpload={vi.fn()} />
        </QueryClientProvider>
      )

      // 该文案同时出现在筛选按钮上，且算法名可能包含该文案（如「目标检测算法」），
      // 因此用 exact 定位到表格里只有徽章的那个单元格
      const cell = getByRole('cell', { name: label, exact: true })
      await expect.element(cell).toBeInTheDocument()
      const badge = (cell.element() as HTMLElement).querySelector(
        '[data-slot="badge"]'
      ) as HTMLElement
      expect(badge).not.toBeNull()

      // 1. 必须取用 §1.3.6 该槽位的类串（单一真实源）
      expectClassesApplied(badge, CATEGORY_BADGE_CLASS[hue])
      // 2. 类名必须真的渲染成对应语义 token，light / dark 双模各自成立
      await expectPaletteRendersAs(badge, CATEGORY_BADGE_CLASS[hue])
      await expectDiffersInDarkMode(badge, 'borderTopColor')

      expect(badge.className).not.toMatch(/(bg|border|text)-(zinc|slate|gray)-/)
    }
  )
})
