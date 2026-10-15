import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render } from 'vitest-browser-react'
import { type Algorithm } from '../data/schema'
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
}))

describe('AlgorithmsTable', () => {
  const queryClient = new QueryClient()

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

    await expect
      .element(getByText(/尚未安装任何算法包/i))
      .toBeInTheDocument()
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

    await expect
      .element(getByText('人脸识别算法'))
      .toBeInTheDocument()

    await expect
      .element(getByText('v1.0.0'))
      .toBeInTheDocument()

    await expect
      .element(getByText('face_recognition'))
      .toBeInTheDocument()
  })
})
