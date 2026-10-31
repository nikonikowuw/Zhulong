import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render } from 'vitest-browser-react'
import { type Algorithm } from '../data/schema'
import { AlgorithmDetailSheet } from './algorithm-detail-sheet'
import { AlgorithmsProvider } from './algorithms-provider'

const mockToastSuccess = vi.hoisted(() => vi.fn())
const mockToastError = vi.hoisted(() => vi.fn())
const mockCopyToClipboard = vi.hoisted(() => vi.fn().mockResolvedValue(true))

vi.mock('@/lib/clipboard', () => ({
  copyToClipboard: mockCopyToClipboard,
}))

vi.mock('sonner', () => ({
  toast: {
    success: mockToastSuccess,
    error: mockToastError,
  },
}))

const sampleAlgorithm: Algorithm = {
  id: 'helmet_detector',
  name: '安全帽佩戴检测',
  version: '1.2.0',
  taskType: 'detection',
  category: 'safety',
  targetArch: 'aarch64',
  targetDevice: 'rknn',
  computeCost: 25,
  memoryMb: 128,
  description: '施工现场高空与作业区域安全帽佩戴实时检测',
  manifestJson: JSON.stringify({
    id: 'helmet_detector',
    name: '安全帽佩戴检测',
    version: '1.2.0',
    labels: ['person', 'helmet', 'head'],
    platform: { arch: 'aarch64', target: 'rknn' },
    resources: { compute_cost: 25, memory_mb: 128 },
  }),
  configSchemaJson: JSON.stringify({
    type: 'object',
    required: ['confidence_threshold'],
    properties: {
      confidence_threshold: {
        type: 'number',
        title: '置信度门限',
        description: '过滤低置信度检测框',
        default: 0.45,
        minimum: 0.1,
        maximum: 0.9,
        step: 0.05,
      },
    },
  }),
  supportsMotionGate: true,
  status: 'DISABLED',
  previousVersion: '1.1.0',
  createdAt: '2026-10-15T08:00:00Z',
  updatedAt: '2026-10-15T09:30:00Z',
}

describe('AlgorithmDetailSheet', () => {
  const queryClient = new QueryClient()

  beforeEach(() => {
    vi.clearAllMocks()
  })

  const renderSheet = (
    algo: Algorithm | null = sampleAlgorithm,
    open = true,
    onOpenTrial = vi.fn()
  ) =>
    render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmsProvider>
          <AlgorithmDetailSheet
            algorithm={algo}
            open={open}
            onOpenChange={vi.fn()}
            onOpenTrial={onOpenTrial}
          />
        </AlgorithmsProvider>
      </QueryClientProvider>
    )

  it('renders header information correctly', async () => {
    const { getByRole, getByText } = await renderSheet()

    await expect
      .element(getByRole('heading', { name: '安全帽佩戴检测' }))
      .toBeInTheDocument()
    await expect.element(getByText('v1.2.0')).toBeInTheDocument()
    await expect
      .element(getByText('施工现场高空与作业区域安全帽佩戴实时检测'))
      .toBeInTheDocument()
  })

  it('renders manifest labels and raw code in Manifest tab', async () => {
    const { getByText, getByTestId } = await renderSheet()

    await expect
      .element(getByText('person', { exact: true }))
      .toBeInTheDocument()
    await expect
      .element(getByText('helmet', { exact: true }))
      .toBeInTheDocument()
    await expect.element(getByText('head', { exact: true })).toBeInTheDocument()

    const codeBlock = getByTestId('manifest-code-block')
    await expect.element(codeBlock).toBeInTheDocument()
    await expect.element(codeBlock).toHaveTextContent('"labels"')
  })

  it('copies manifest JSON when copy button is clicked', async () => {
    const { getByRole } = await renderSheet()

    const copyBtn = getByRole('button', { name: /复制 JSON/i })
    await copyBtn.click()

    expect(mockCopyToClipboard).toHaveBeenCalled()
    expect(mockToastSuccess).toHaveBeenCalled()
  })

  it('renders parameter specifications in Config Schema tab', async () => {
    const { getByRole, getByText } = await renderSheet()

    // 切换至参数规范 Tab
    const schemaTabTrigger = getByRole('tab', { name: /参数规范/i })
    await schemaTabTrigger.click()

    await expect
      .element(getByText('置信度门限', { exact: true }))
      .toBeInTheDocument()
    await expect
      .element(getByText('0.1 ~ 0.9 (步长 0.05)', { exact: true }))
      .toBeInTheDocument()
    await expect.element(getByText('0.45', { exact: true })).toBeInTheDocument()
    await expect.element(getByText('必填', { exact: true })).toBeInTheDocument()
  })

  it('renders zero-config alert when schema is empty', async () => {
    const zeroConfigAlgo: Algorithm = {
      ...sampleAlgorithm,
      configSchemaJson: '{}',
    }
    const { getByRole, getByText } = await renderSheet(zeroConfigAlgo)

    const schemaTabTrigger = getByRole('tab', { name: /参数规范/i })
    await schemaTabTrigger.click()

    await expect.element(getByText('零参算法')).toBeInTheDocument()
  })

  it('renders runtime info and trial shortcut in Runtime tab', async () => {
    const handleTrial = vi.fn()
    const { getByRole, getByText } = await renderSheet(
      sampleAlgorithm,
      true,
      handleTrial
    )

    const runtimeTabTrigger = getByRole('tab', { name: /运行与版本/i })
    await runtimeTabTrigger.click()

    await expect.element(getByText('v1.1.0')).toBeInTheDocument()

    const trialBtn = getByRole('button', { name: /前往效果试跑/i })
    await expect.element(trialBtn).toBeInTheDocument()
    await trialBtn.click()
    expect(handleTrial).toHaveBeenCalledWith(sampleAlgorithm)
  })

  it('returns null when algorithm is null', async () => {
    const { container } = await renderSheet(null)
    expect(container.innerHTML).toBe('')
  })
})
