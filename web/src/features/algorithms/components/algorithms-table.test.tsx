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
import { AlgorithmsDialogs } from './algorithms-dialogs'
import { AlgorithmsProvider } from './algorithms-provider'
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
  useTrialAlgorithm: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useInstallAlgorithm: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
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

vi.mock('@/hooks/use-table-url-state', () => ({
  useTableUrlState: () => ({
    pagination: { pageIndex: 0, pageSize: 10 },
    onPaginationChange: vi.fn(),
    ensurePageInRange: vi.fn(),
    globalFilter: '',
    onGlobalFilterChange: vi.fn(),
    columnFilters: [],
    onColumnFiltersChange: vi.fn(),
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
    supportsMotionGate: true,
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

  const renderTable = (
    props: {
      data?: Algorithm[]
      isLoading?: boolean
      isError?: boolean
      onOpenUpload?: () => void
    } = {}
  ) => {
    return render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmsProvider>
          <AlgorithmsTable
            data={props.data ?? mockAlgorithmsData}
            isLoading={props.isLoading ?? mockIsLoading}
            isError={props.isError ?? false}
            onRetry={vi.fn()}
            search={{}}
            navigate={vi.fn()}
            onOpenUpload={props.onOpenUpload}
          />
        </AlgorithmsProvider>
      </QueryClientProvider>
    )
  }

  it('renders empty state when no algorithms exist', async () => {
    mockAlgorithmsData = []
    const { getByText } = await renderTable({ data: [] })

    await expect.element(getByText(/尚未安装任何算法包/i)).toBeInTheDocument()
  })

  it('renders table rows when algorithms exist', async () => {
    const algos = [
      makeAlgorithm({
        id: 'face_recognition',
        name: '人脸识别算法',
        version: '1.0.0',
        status: 'ACTIVE',
      }),
    ]

    const { getByText } = await renderTable({ data: algos })

    await expect.element(getByText('人脸识别算法')).toBeInTheDocument()
    await expect.element(getByText('v1.0.0')).toBeInTheDocument()
    await expect.element(getByText('face_recognition')).toBeInTheDocument()
  })

  it('enables a disabled algorithm when the scheduling switch is turned on', async () => {
    const algos = [makeAlgorithm({ id: 'algo_off', status: 'DISABLED' })]

    const { getByRole } = await renderTable({ data: algos })

    const toggle = getByRole('switch')
    await expect.element(toggle).toBeInTheDocument()
    await toggle.click()

    expect(mockEnableMutate).toHaveBeenCalledWith('algo_off')
    expect(mockDisableMutate).not.toHaveBeenCalled()
  })

  it('disables an active algorithm when the scheduling switch is turned off', async () => {
    const algos = [makeAlgorithm({ id: 'algo_on', status: 'ACTIVE' })]

    const { getByRole } = await renderTable({ data: algos })

    const toggle = getByRole('switch')
    await expect.element(toggle).toBeInTheDocument()
    await toggle.click()

    expect(mockDisableMutate).toHaveBeenCalledWith('algo_on')
    expect(mockEnableMutate).not.toHaveBeenCalled()
  })

  it('offers the reset-breaker action only for a crash-loop algorithm', async () => {
    const active = await renderTable({
      data: [makeAlgorithm({ status: 'ACTIVE' })],
    })
    expect(active.container.textContent).not.toContain('重置熔断')

    const crashed = await renderTable({
      data: [makeAlgorithm({ status: 'CRASH_LOOP' })],
    })
    await expect
      .element(crashed.getByRole('button', { name: /重置熔断/i }))
      .toBeInTheDocument()
  })

  it('disables the scheduling switch for a crash-loop algorithm', async () => {
    const algos = [makeAlgorithm({ id: 'algo_stuck', status: 'CRASH_LOOP' })]

    const { getByRole } = await renderTable({ data: algos })

    const toggle = getByRole('switch')
    await expect.element(toggle).toBeDisabled()
    expect(toggle.element().getAttribute('title')).toContain('熔断')
  })

  it('disables the scheduling switch for a starting algorithm', async () => {
    const algos = [makeAlgorithm({ id: 'algo_starting', status: 'STARTING' })]

    const { getByRole } = await renderTable({ data: algos })

    const toggle = getByRole('switch')
    await expect.element(toggle).toBeDisabled()
    expect(toggle.element().getAttribute('title')).toContain('切换中')
  })

  it('disables the scheduling switch for a rolling-back algorithm', async () => {
    const algos = [makeAlgorithm({ id: 'algo_rb', status: 'ROLLING_BACK' })]

    const { getByRole } = await renderTable({ data: algos })

    const toggle = getByRole('switch')
    await expect.element(toggle).toBeDisabled()
    expect(toggle.element().getAttribute('title')).toContain('切换中')
  })

  const TASK_TYPE_HUE: Array<[AlgorithmTaskType, string, CategoryHue]> = [
    ['detection', '目标检测', 'blue'],
    ['classification', '图像分类', 'violet'],
    ['person_structure', '结构化人脸', 'emerald'],
    ['vehicle_structure', '机动车结构化', 'fuchsia'],
  ]

  it.each(TASK_TYPE_HUE)(
    'renders the %s task-type badge on the %s §1.3.6 slot',
    async (taskType, label, hue) => {
      const algos = [makeAlgorithm({ taskType, name: `${label}算法` })]

      const { getByRole } = await renderTable({ data: algos })

      const cell = getByRole('cell', { name: label, exact: true })
      await expect.element(cell).toBeInTheDocument()
      const badge = (cell.element() as HTMLElement).querySelector(
        '[data-slot="badge"]'
      ) as HTMLElement
      expect(badge).not.toBeNull()

      expectClassesApplied(badge, CATEGORY_BADGE_CLASS[hue])
      await expectPaletteRendersAs(badge, CATEGORY_BADGE_CLASS[hue])
      await expectDiffersInDarkMode(badge, 'borderTopColor')

      expect(badge.className).not.toMatch(/(bg|border|text)-(zinc|slate|gray)-/)
    }
  )

  it('opens detail sheet when clicking on a table row', async () => {
    const algos = [
      makeAlgorithm({
        id: 'face_recognition',
        name: '人脸识别算法',
        manifestJson: JSON.stringify({
          id: 'face_recognition',
          labels: ['face'],
        }),
      }),
    ]

    const { getByRole } = await render(
      <QueryClientProvider client={new QueryClient()}>
        <AlgorithmsProvider>
          <AlgorithmsTable
            data={algos}
            isLoading={false}
            isError={false}
            onRetry={vi.fn()}
            search={{}}
            navigate={vi.fn()}
          />
          <AlgorithmsDialogs />
        </AlgorithmsProvider>
      </QueryClientProvider>
    )

    const row = getByRole('row', { name: /face_recognition/i })
    await row.click()

    await expect
      .element(getByRole('heading', { name: '人脸识别算法' }))
      .toBeInTheDocument()
  })

  it('opens delete dialog and not detail sheet when clicking delete in actions menu', async () => {
    const algos = [
      makeAlgorithm({
        id: 'face_recognition',
        name: '人脸识别算法',
        status: 'DISABLED',
        manifestJson: JSON.stringify({
          id: 'face_recognition',
          labels: ['face'],
        }),
      }),
    ]

    const { getByRole } = await render(
      <QueryClientProvider client={new QueryClient()}>
        <AlgorithmsProvider>
          <AlgorithmsTable
            data={algos}
            isLoading={false}
            isError={false}
            onRetry={vi.fn()}
            search={{}}
            navigate={vi.fn()}
          />
          <AlgorithmsDialogs />
        </AlgorithmsProvider>
      </QueryClientProvider>
    )

    const actionBtn = getByRole('button', { name: /操作/i })
    await actionBtn.click()

    const deleteItem = getByRole('menuitem', { name: /卸载算法包/i })
    await deleteItem.click()

    // 应该弹出卸载确认弹窗（alertdialog），而不是算法详情（dialog）
    await expect.element(getByRole('alertdialog')).toBeInTheDocument()
    await expect
      .element(getByRole('button', { name: '确认卸载' }))
      .toBeInTheDocument()
    await expect.element(getByRole('dialog')).not.toBeInTheDocument()
  })
})
