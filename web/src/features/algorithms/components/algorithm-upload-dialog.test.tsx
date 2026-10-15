import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render } from 'vitest-browser-react'
import { AlgorithmUploadDialog } from './algorithm-upload-dialog'

const mockInstallMutate = vi.fn()

vi.mock('../hooks/use-algorithms', () => ({
  useInstallAlgorithm: () => ({
    mutate: mockInstallMutate,
    isPending: false,
  }),
}))

describe('AlgorithmUploadDialog', () => {
  const queryClient = new QueryClient()

  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders upload dialog elements correctly when opened', async () => {
    const { getByRole, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <AlgorithmUploadDialog open={true} onOpenChange={vi.fn()} />
      </QueryClientProvider>
    )

    await expect
      .element(getByRole('heading', { name: /安装算法包/i }))
      .toBeInTheDocument()

    await expect
      .element(getByText(/点击选择或将算法包拖拽至此处/i))
      .toBeInTheDocument()

    await expect
      .element(getByRole('button', { name: /开始安装/i }))
      .toBeInTheDocument()
  })
})
