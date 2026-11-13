import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { toast } from 'sonner'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { Button } from '@/components/ui/button'
import { TooltipProvider } from '@/components/ui/tooltip'
import { personsApi } from '../api/persons-api'
import { type FaceReextractJobResponse } from '../data/schema'
import { PersonsFaceReextractDialog } from './persons-face-reextract-dialog'
import { PersonsProvider, usePersonsActions } from './persons-provider'

vi.mock('../api/persons-api', () => ({
  personsApi: {
    createFaceReextractJob: vi.fn(),
    getLatestFaceReextractJob: vi.fn(),
    getFaceReextractJob: vi.fn(),
    cancelFaceReextractJob: vi.fn(),
    getFaceReextractFailedReportUrl: vi.fn(
      (jobId: string) =>
        `/api/v1/persons/reextract-face-jobs/${jobId}/failed-report`
    ),
  },
}))

vi.mock('sonner', () => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
    info: vi.fn(),
  },
}))

function Harness() {
  const { openFaceReextract } = usePersonsActions()
  return (
    <>
      <Button onClick={() => openFaceReextract(null)}>Reextract All</Button>
      <Button onClick={() => openFaceReextract({ personIds: ['p1', 'p2'] })}>
        Reextract Selected
      </Button>
      <PersonsFaceReextractDialog />
    </>
  )
}

function makeJob(
  overrides: Partial<FaceReextractJobResponse> = {}
): FaceReextractJobResponse {
  return {
    jobId: 're-job-100',
    scope: 'all',
    status: 'PENDING',
    progress: 0,
    total: 5,
    processed: 0,
    succeeded: 0,
    failed: 0,
    currentStage: '',
    currentFile: '',
    errorCode: '',
    errorMessage: '',
    createdAt: '2026-11-12T00:00:00Z',
    updatedAt: '2026-11-12T00:00:00Z',
    finishedAt: null,
    ...overrides,
  }
}

describe('PersonsFaceReextractDialog', () => {
  let queryClient: QueryClient

  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(personsApi.getLatestFaceReextractJob).mockResolvedValue(null)
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })
  })

  it('starts full-gallery re-extraction and shows completion with failed report download', async () => {
    const pendingJob = makeJob({ status: 'PENDING' })
    const completedJob = makeJob({
      status: 'COMPLETED',
      progress: 100,
      total: 5,
      processed: 5,
      succeeded: 4,
      failed: 1,
      finishedAt: '2026-11-12T00:01:00Z',
    })
    vi.mocked(personsApi.createFaceReextractJob).mockResolvedValueOnce(
      pendingJob
    )
    vi.mocked(personsApi.getFaceReextractJob).mockResolvedValue(completedJob)

    const { getByRole, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <TooltipProvider>
          <PersonsProvider>
            <Harness />
          </PersonsProvider>
        </TooltipProvider>
      </QueryClientProvider>
    )

    await getByRole('button', { name: 'Reextract All' }).click()
    await expect
      .element(getByText(/全库所有已录入人脸照片/i))
      .toBeInTheDocument()

    await getByRole('button', { name: /开始重新提取/i }).click()

    await expect
      .poll(
        () => vi.mocked(personsApi.createFaceReextractJob).mock.calls.length
      )
      .toBe(1)
    expect(personsApi.createFaceReextractJob).toHaveBeenCalledWith(undefined)
    expect(toast.success).toHaveBeenCalled()

    await expect
      .element(getByText(/成功更新 4 条特征向量；1 条提取失败/i))
      .toBeInTheDocument()
    await expect
      .element(getByRole('button', { name: /下载失败报告/i }))
      .toBeInTheDocument()
  })

  it('passes selected personIds and supports cancelling a running job', async () => {
    const runningJob = makeJob({
      jobId: 're-job-200',
      scope: 'selected',
      status: 'PROCESSING',
      progress: 40,
      total: 4,
      processed: 2,
      succeeded: 2,
      failed: 0,
      currentFile: '张三 (a.jpg)',
    })
    vi.mocked(personsApi.createFaceReextractJob).mockResolvedValueOnce(
      runningJob
    )
    vi.mocked(personsApi.getFaceReextractJob).mockResolvedValue(runningJob)
    vi.mocked(personsApi.cancelFaceReextractJob).mockResolvedValueOnce(true)

    const { getByRole, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <TooltipProvider>
          <PersonsProvider>
            <Harness />
          </PersonsProvider>
        </TooltipProvider>
      </QueryClientProvider>
    )

    await getByRole('button', { name: 'Reextract Selected' }).click()
    await expect
      .element(getByText(/已选中的 2 位人员的全部人脸照片/i))
      .toBeInTheDocument()

    await getByRole('button', { name: /开始重新提取/i }).click()
    await expect
      .poll(
        () => vi.mocked(personsApi.createFaceReextractJob).mock.calls.length
      )
      .toBe(1)
    expect(personsApi.createFaceReextractJob).toHaveBeenCalledWith(['p1', 'p2'])

    await expect.element(getByText(/张三 \(a\.jpg\)/)).toBeInTheDocument()

    await getByRole('button', { name: /取消任务/i }).click()
    await expect
      .poll(
        () => vi.mocked(personsApi.cancelFaceReextractJob).mock.calls.length
      )
      .toBe(1)
    expect(personsApi.cancelFaceReextractJob).toHaveBeenCalledWith('re-job-200')
  })
})
