import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { Button } from '@/components/ui/button'
import { personsApi } from '../api/persons-api'
import { type Face, type Person } from '../data/schema'
import { PersonsFacesDialog } from './persons-faces-dialog'
import { PersonsProvider, usePersonsContext } from './persons-provider'

const mockFacesInitial: Face[] = [
  {
    id: 'f-01',
    ownerRowId: 'p-01',
    imagePath: '/data/faces/alice_01.jpg',
    createdAt: '2026-10-14T01:00:00Z',
  },
  {
    id: 'f-02',
    ownerRowId: 'p-01',
    imagePath: '/data/faces/alice_02.jpg',
    createdAt: '2026-10-14T02:00:00Z',
  },
]

const mockPerson: Person = {
  id: 'p-01',
  personId: 'EMP001',
  name: 'Alice Cooper',
  source: 'LOCAL',
  sourceName: '',
  primaryFaceId: 'f-01',
  faces: mockFacesInitial,
  createdAt: '2026-10-14T00:00:00Z',
  updatedAt: '2026-10-14T00:00:00Z',
}

vi.mock('../api/persons-api', () => ({
  personsApi: {
    getPerson: vi.fn(),
    listFaces: vi.fn(),
    deleteFace: vi.fn(),
    setPrimaryFace: vi.fn(),
    addFace: vi.fn(),
    getLatestFaceReextractJob: vi.fn(),
  },
}))

function TestComponent({ person = mockPerson }: { person?: Person }) {
  const { setOpen, setCurrentRow } = usePersonsContext()
  const handleOpen = () => {
    setCurrentRow(person)
    setOpen('faces')
  }
  return (
    <div>
      <Button onClick={handleOpen}>Open Faces</Button>
      <PersonsFacesDialog />
    </div>
  )
}

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
}

describe('PersonsFacesDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(personsApi.getLatestFaceReextractJob).mockResolvedValue(null)
    vi.mocked(personsApi.getPerson).mockResolvedValue(mockPerson)
    vi.mocked(personsApi.listFaces).mockResolvedValue({
      items: mockFacesInitial,
      total: mockFacesInitial.length,
      page: 1,
      pageSize: 100,
    })
  })

  it('renders face list and add face form when opened', async () => {
    const queryClient = createTestQueryClient()

    const { getByRole, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <TestComponent />
        </PersonsProvider>
      </QueryClientProvider>
    )

    await getByRole('button', { name: /Open Faces/i }).click()

    await expect
      .element(getByRole('heading', { name: /人脸底库管理 - Alice Cooper/i }))
      .toBeInTheDocument()

    await expect.element(getByText('alice_01.jpg')).toBeInTheDocument()
    await expect
      .element(getByText('主人脸', { exact: true }))
      .toBeInTheDocument()
    await expect
      .element(getByText(/点击选择或将人脸照片拖拽至此处/i))
      .toBeInTheDocument()
  })

  it('shows "未绑定工号" instead of "未录入底库" when personId is empty', async () => {
    const unboundPerson: Person = {
      ...mockPerson,
      personId: null,
    }
    vi.mocked(personsApi.getPerson).mockResolvedValue(unboundPerson)

    const { getByRole, getByText } = await render(
      <QueryClientProvider client={createTestQueryClient()}>
        <PersonsProvider>
          <TestComponent person={unboundPerson} />
        </PersonsProvider>
      </QueryClientProvider>
    )

    await getByRole('button', { name: /Open Faces/i }).click()
    await expect.element(getByText('未绑定工号')).toBeInTheDocument()
    expect(document.body.textContent).not.toContain('未录入底库')
  })

  it('promotes the remaining face as primary after deleting the primary face', async () => {
    let facesState = [...mockFacesInitial]
    let personState = { ...mockPerson }

    vi.mocked(personsApi.getPerson).mockImplementation(async () => personState)
    vi.mocked(personsApi.listFaces).mockImplementation(async () => ({
      items: facesState,
      total: facesState.length,
      page: 1,
      pageSize: 100,
    }))
    vi.mocked(personsApi.deleteFace).mockImplementation(async (_pid, fid) => {
      facesState = facesState.filter((f) => f.id !== fid)
      personState = {
        ...personState,
        primaryFaceId: facesState[0]?.id ?? null,
        faces: facesState,
      }
    })

    const { getByRole, getByText } = await render(
      <QueryClientProvider client={createTestQueryClient()}>
        <PersonsProvider>
          <TestComponent />
        </PersonsProvider>
      </QueryClientProvider>
    )

    await getByRole('button', { name: /Open Faces/i }).click()
    await expect.element(getByText('alice_01.jpg')).toBeInTheDocument()
    await expect.element(getByText('alice_02.jpg')).toBeInTheDocument()

    // 删除第一张（即当前主人脸 f-01）
    const deleteButtons = getByRole('button', { name: '删除人脸' })
    await deleteButtons.first().click()

    await expect
      .poll(() => vi.mocked(personsApi.deleteFace).mock.calls.length)
      .toBe(1)

    // 剩余的 alice_02.jpg 应承接主人脸标识，不再出现旧主图和"设为主人脸"按钮
    await expect.element(getByText('alice_02.jpg')).toBeInTheDocument()
    await expect.element(getByText('f-02', { exact: true })).toBeInTheDocument()
    await expect
      .element(getByText('主人脸', { exact: true }))
      .toBeInTheDocument()
    expect(document.body.textContent).not.toContain('alice_01.jpg')
    expect(document.body.textContent).not.toContain('设为主人脸')
  })

  it('uses a native button for the drag-drop upload zone for keyboard accessibility', async () => {
    const { getByRole } = await render(
      <QueryClientProvider client={createTestQueryClient()}>
        <PersonsProvider>
          <TestComponent />
        </PersonsProvider>
      </QueryClientProvider>
    )

    await getByRole('button', { name: /Open Faces/i }).click()
    const uploadZoneBtn = getByRole('button', {
      name: /点击选择或将人脸照片拖拽至此处/i,
    })
    await expect.element(uploadZoneBtn).toBeInTheDocument()
  })
})
