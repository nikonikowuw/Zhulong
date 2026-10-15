import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render } from 'vitest-browser-react'
import { type Algorithm } from '../data/schema'
import { AlgorithmDeleteDialog } from './algorithm-delete-dialog'

const mockMutate = vi.fn()

vi.mock('../hooks/use-algorithms', () => ({
  useDeleteAlgorithm: () => ({
    mutate: mockMutate,
    isPending: false,
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
