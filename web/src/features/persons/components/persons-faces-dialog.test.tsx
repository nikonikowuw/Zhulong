import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { Button } from '@/components/ui/button'
import { type Person } from '../data/schema'
import { PersonsFacesDialog } from './persons-faces-dialog'
import { PersonsProvider, usePersonsContext } from './persons-provider'

const mockPerson: Person = {
  id: 'p-01',
  personId: 'EMP001',
  name: 'Alice Cooper',
  faces: [],
  createdAt: '2026-10-14T00:00:00Z',
  updatedAt: '2026-10-14T00:00:00Z',
}

vi.mock('../hooks/use-persons', () => ({
  usePersonFaces: () => ({
    data: {
      items: [
        {
          id: 'f-01',
          ownerRowId: 'p-01',
          imagePath: '/data/faces/alice_01.jpg',
          createdAt: '2026-10-14T01:00:00Z',
        },
      ],
      total: 1,
      page: 1,
      pageSize: 100,
    },
    isLoading: false,
  }),
  useAddFace: () => ({
    mutateAsync: vi.fn(),
    isPending: false,
  }),
  useDeleteFace: () => ({
    mutateAsync: vi.fn(),
    isPending: false,
  }),
  useSetPrimaryFace: () => ({
    mutateAsync: vi.fn(),
    isPending: false,
  }),
}))

function TestComponent() {
  const { setOpen, setCurrentRow } = usePersonsContext()
  const handleOpen = () => {
    setCurrentRow(mockPerson)
    setOpen('faces')
  }
  return (
    <div>
      <Button onClick={handleOpen}>Open Faces</Button>
      <PersonsFacesDialog />
    </div>
  )
}

describe('PersonsFacesDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders face list and add face form when opened', async () => {
    const queryClient = new QueryClient()

    const { getByRole, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <TestComponent />
        </PersonsProvider>
      </QueryClientProvider>
    )

    const openBtn = getByRole('button', { name: /Open Faces/i })
    await openBtn.click()

    await expect
      .element(getByRole('heading', { name: /人脸底库管理 - Alice Cooper/i }))
      .toBeInTheDocument()

    await expect
      .element(getByText('alice_01.jpg'))
      .toBeInTheDocument()

    // 默认首张人脸标记为主图
    await expect
      .element(getByText(/主人脸/i))
      .toBeInTheDocument()

    await expect
      .element(getByText(/点击选择或将人脸照片拖拽至此处/i))
      .toBeInTheDocument()
  })

  it('renders face card with title and preview properly', async () => {
    const queryClient = new QueryClient()

    const { getByRole, getByTestId } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <TestComponent />
        </PersonsProvider>
      </QueryClientProvider>
    )

    const openBtn = getByRole('button', { name: /Open Faces/i })
    await openBtn.click()

    const cardElem = getByTestId('face-card')
    await expect.element(cardElem).toBeInTheDocument()
  })
})
