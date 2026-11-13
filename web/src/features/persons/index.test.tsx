import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { toast } from 'sonner'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { TooltipProvider } from '@/components/ui/tooltip'
import { personsApi } from './api/persons-api'
import { type FaceReextractJobResponse, type Person } from './data/schema'
import { Persons } from './index'

let mockSearch: Record<string, unknown> = {}
const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    getRouteApi: (id: string) => {
      if (id === '/_authenticated/persons/') {
        return {
          useSearch: () => mockSearch,
          useNavigate: () => mockNavigate,
        }
      }
      return actual.getRouteApi(id as never)
    },
  }
})

vi.mock('@/components/layout/header', () => ({
  Header: ({ children }: { children: React.ReactNode }) => (
    <header>{children}</header>
  ),
}))
vi.mock('@/components/layout/main', () => ({
  Main: ({ children }: { children: React.ReactNode }) => (
    <main>{children}</main>
  ),
}))
vi.mock('@/components/search', () => ({ Search: () => null }))
vi.mock('@/components/language-switch', () => ({ LanguageSwitch: () => null }))
vi.mock('@/components/theme-switch', () => ({ ThemeSwitch: () => null }))
vi.mock('@/components/config-drawer', () => ({ ConfigDrawer: () => null }))
vi.mock('@/components/profile-dropdown', () => ({
  ProfileDropdown: () => null,
}))

vi.mock('./api/persons-api', () => ({
  personsApi: {
    getPersons: vi.fn(),
    getPerson: vi.fn(),
    listFaces: vi.fn(),
    getLatestFaceReextractJob: vi.fn(),
    getFaceReextractJob: vi.fn(),
    createFaceReextractJob: vi.fn(),
    cancelFaceReextractJob: vi.fn(),
    getFaceReextractFailedReportUrl: vi.fn((id: string) => `/failed/${id}`),
  },
}))

vi.mock('sonner', () => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
    info: vi.fn(),
  },
}))

const samplePerson: Person = {
  id: 'p-42',
  personId: 'EMP042',
  name: 'Deep Link User',
  source: 'LOCAL',
  sourceName: '',
  faces: [],
  createdAt: '2026-10-14T00:00:00Z',
  updatedAt: '2026-10-14T00:00:00Z',
}

function createClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
}

describe('Persons page integration', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockSearch = {}
    vi.mocked(personsApi.getPersons).mockResolvedValue({
      items: [samplePerson],
      total: 1,
      page: 1,
      pageSize: 10,
    })
    vi.mocked(personsApi.listFaces).mockResolvedValue({
      items: [],
      total: 0,
      page: 1,
      pageSize: 100,
    })
    vi.mocked(personsApi.getLatestFaceReextractJob).mockResolvedValue(null)
  })

  it('opens faces drawer for ?personId and clears the search param on close', async () => {
    mockSearch = { personId: 'p-42' }
    vi.mocked(personsApi.getPerson).mockResolvedValue(samplePerson)

    const { getByRole } = await render(
      <QueryClientProvider client={createClient()}>
        <TooltipProvider>
          <Persons />
        </TooltipProvider>
      </QueryClientProvider>
    )

    await expect
      .element(getByRole('heading', { name: /人脸底库管理 - Deep Link User/i }))
      .toBeInTheDocument()

    // 关闭抽屉
    await getByRole('button', { name: 'Close' }).click()

    await expect.poll(() => mockNavigate.mock.calls.length).toBeGreaterThan(0)
    const updater = mockNavigate.mock.calls[0][0].search as (
      prev: Record<string, unknown>
    ) => Record<string, unknown>
    expect(updater({ personId: 'p-42', page: 2 })).toEqual({ page: 2 })
  })

  it('shows an error toast and removes invalid ?personId when deep-link fetch fails', async () => {
    mockSearch = { personId: 'missing-id' }
    vi.mocked(personsApi.getPerson).mockRejectedValue(
      new Error('Person not found')
    )

    await render(
      <QueryClientProvider client={createClient()}>
        <TooltipProvider>
          <Persons />
        </TooltipProvider>
      </QueryClientProvider>
    )

    await expect.poll(() => mockNavigate.mock.calls.length).toBeGreaterThan(0)
    expect(toast.error).toHaveBeenCalledWith('Person not found')
    const call = mockNavigate.mock.calls[0][0]
    expect(call.replace).toBe(true)
    const updater = call.search as (
      prev: Record<string, unknown>
    ) => Record<string, unknown>
    expect(updater({ personId: 'missing-id', view: 'grid' })).toEqual({
      view: 'grid',
    })
  })

  it('resumes an active reextract job once on mount and does not bounce back when clicking retry-another after completion', async () => {
    const activeJob: FaceReextractJobResponse = {
      jobId: 're-active-1',
      scope: 'all',
      status: 'PROCESSING',
      progress: 50,
      total: 2,
      processed: 1,
      succeeded: 1,
      failed: 0,
      currentStage: 'PROCESSING',
      currentFile: 'a.jpg',
      errorCode: '',
      errorMessage: '',
      createdAt: '2026-11-12T00:00:00Z',
      updatedAt: '2026-11-12T00:00:01Z',
      finishedAt: null,
    }
    const completedJob: FaceReextractJobResponse = {
      ...activeJob,
      status: 'COMPLETED',
      progress: 100,
      processed: 2,
      succeeded: 2,
      currentFile: '',
      finishedAt: '2026-11-12T00:00:02Z',
    }

    // 即使 latest 仍返回旧的 PROCESSING 缓存/响应，页面生命期内也只做一次挂载恢复；
    // 点击「再次重提」后绝不能把旧任务重新挂回去。
    vi.mocked(personsApi.getLatestFaceReextractJob).mockResolvedValue(activeJob)
    vi.mocked(personsApi.getFaceReextractJob).mockResolvedValue(completedJob)

    const { getByRole, getByText } = await render(
      <QueryClientProvider client={createClient()}>
        <TooltipProvider>
          <Persons />
        </TooltipProvider>
      </QueryClientProvider>
    )

    // 等恢复逻辑把任务挂上后打开「重提特征」弹窗
    await expect
      .poll(() => vi.mocked(personsApi.getFaceReextractJob).mock.calls.length)
      .toBeGreaterThan(0)

    await getByRole('button', { name: /重提特征/i }).click()
    await expect.element(getByText(/人脸特征重新提取完成/i)).toBeInTheDocument()

    // 点击「再次重提」回到确认页；页面不应再把旧 jobId 重新设回来
    await getByRole('button', { name: /再次重提/i }).click()
    await expect
      .element(getByRole('button', { name: /开始重新提取/i }))
      .toBeInTheDocument()
  })
})
