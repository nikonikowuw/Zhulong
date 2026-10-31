import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render } from 'vitest-browser-react'
import { type Algorithm } from '../data/schema'
import { AlgorithmTrialDialog } from './algorithm-trial-dialog'

const mockTrialMutate = vi.fn()

vi.mock('../hooks/use-algorithms', () => ({
  useTrialAlgorithm: () => ({
    mutate: mockTrialMutate,
    isPending: false,
  }),
}))

const mockAlgorithm: Algorithm = {
  id: 'helmet_detector',
  name: '安全帽识别算法',
  version: '1.2.0',
  taskType: 'detection',
  category: 'safety',
  targetArch: 'x86_64',
  targetDevice: 'onnx',
  computeCost: 15,
  memoryMb: 64,
  description: '检测施工作业人员佩戴安全帽情况',
  manifestJson: '{}',
  configSchemaJson: '{}',
  supportsMotionGate: true,
  status: 'DISABLED',
  previousVersion: '',
  createdAt: '2026-03-31T00:00:00Z',
  updatedAt: '2026-03-31T00:00:00Z',
}

describe('AlgorithmTrialDialog', () => {
  const queryClient = new QueryClient()

  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders trial dialog with algorithm details and dropzone when opened', async () => {
    const { getByRole, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmTrialDialog
          algorithm={mockAlgorithm}
          open={true}
          onOpenChange={vi.fn()}
        />
      </QueryClientProvider>
    )

    await expect
      .element(getByRole('heading', { name: /算法效果试跑 - 安全帽识别算法/i }))
      .toBeInTheDocument()

    await expect
      .element(getByText(/点击选择或将图片拖拽至此处/i))
      .toBeInTheDocument()

    await expect
      .element(getByText(/支持 JPG \/ JPEG \/ PNG 格式/i))
      .toBeInTheDocument()

    await expect
      .element(getByRole('button', { name: /取消/i }))
      .toBeInTheDocument()
  })

  it('triggers onOpenChange when cancel button is clicked', async () => {
    const onOpenChange = vi.fn()
    const { getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmTrialDialog
          algorithm={mockAlgorithm}
          open={true}
          onOpenChange={onOpenChange}
        />
      </QueryClientProvider>
    )

    const cancelButton = getByRole('button', { name: /取消/i })
    await cancelButton.click()
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })
})
