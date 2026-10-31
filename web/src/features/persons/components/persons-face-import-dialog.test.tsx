import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { toast } from 'sonner'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { Button } from '@/components/ui/button'
import { personsApi } from '../api/persons-api'
import { type FaceImportJobResponse } from '../data/schema'
import { PersonsFaceImportDialog } from './persons-face-import-dialog'
import { PersonsProvider, usePersonsContext } from './persons-provider'

vi.mock('../api/persons-api', () => ({
  personsApi: {
    createFaceImportJob: vi.fn(),
    getFaceImportJob: vi.fn(),
    getFaceImportFailedReportUrl: vi.fn(
      (jobId: string) =>
        `/api/v1/persons/import-face-jobs/${jobId}/failed-report`
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

function TestComponent() {
  const { setOpen } = usePersonsContext()
  return (
    <>
      <Button onClick={() => setOpen('import-faces')}>Open Face Import</Button>
      <PersonsFaceImportDialog />
    </>
  )
}

function createZipFile(name = 'faces.zip') {
  return new File(['dummy-zip-binary'], name, { type: 'application/zip' })
}

describe('PersonsFaceImportDialog', () => {
  let queryClient: QueryClient

  beforeEach(() => {
    vi.clearAllMocks()
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })
  })

  it('opens with the upload prompt and downloads manifest template', async () => {
    const clickSpy = vi
      .spyOn(HTMLAnchorElement.prototype, 'click')
      .mockImplementation(() => {})
    const createUrlSpy = vi
      .spyOn(URL, 'createObjectURL')
      .mockReturnValue('blob:mock-url')
    const revokeUrlSpy = vi
      .spyOn(URL, 'revokeObjectURL')
      .mockImplementation(() => {})

    const { getByRole, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <TestComponent />
        </PersonsProvider>
      </QueryClientProvider>
    )

    await getByRole('button', { name: 'Open Face Import' }).click()

    await expect
      .element(getByRole('heading', { name: /人员人脸压缩包批量导入/i }))
      .toBeInTheDocument()
    await expect
      .element(getByText(/点击选择或拖拽 ZIP 压缩包/i))
      .toBeInTheDocument()
    await expect.element(getByText(/压缩包组织标准/i)).toBeInTheDocument()

    // Download template
    await getByRole('button', { name: /下载清单模板/i }).click()
    expect(createUrlSpy).toHaveBeenCalled()
    expect(clickSpy).toHaveBeenCalled()
    expect(revokeUrlSpy).toHaveBeenCalledWith('blob:mock-url')
  })

  it('rejects non-zip files with an error toast', async () => {
    const { getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <TestComponent />
        </PersonsProvider>
      </QueryClientProvider>
    )

    await getByRole('button', { name: 'Open Face Import' }).click()

    const input = document.querySelector(
      'input[type="file"]'
    ) as HTMLInputElement
    const txtFile = new File(['hello'], 'test.txt', { type: 'text/plain' })
    const dt = new DataTransfer()
    dt.items.add(txtFile)
    input.files = dt.files
    input.dispatchEvent(new Event('change', { bubbles: true }))

    expect(toast.error).toHaveBeenCalledWith('请选择 ZIP 压缩包文件')
    expect(personsApi.createFaceImportJob).not.toHaveBeenCalled()
  })

  it('handles job submission, progress tracking, and failed report download', async () => {
    const pendingJob: FaceImportJobResponse = {
      jobId: 'job_123',
      status: 'PROCESSING',
      progress: 50,
      total: 10,
      processed: 5,
      succeeded: 4,
      failed: 1,
      currentStage: 'PROCESSING',
      currentFile: 'images/005.jpg',
      errorMessage: '',
      createdAt: '2026-10-31T20:00:00Z',
      updatedAt: '2026-10-31T20:00:05Z',
    }

    const completedJob: FaceImportJobResponse = {
      jobId: 'job_123',
      status: 'COMPLETED',
      progress: 100,
      total: 10,
      processed: 10,
      succeeded: 8,
      failed: 2,
      currentStage: 'COMPLETED',
      currentFile: '',
      errorMessage: '',
      createdAt: '2026-10-31T20:00:00Z',
      updatedAt: '2026-10-31T20:00:10Z',
      finishedAt: '2026-10-31T20:00:10Z',
    }

    vi.mocked(personsApi.createFaceImportJob).mockResolvedValue(pendingJob)
    vi.mocked(personsApi.getFaceImportJob).mockResolvedValue(completedJob)

    const clickSpy = vi
      .spyOn(HTMLAnchorElement.prototype, 'click')
      .mockImplementation(() => {})

    const { getByRole, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <TestComponent />
        </PersonsProvider>
      </QueryClientProvider>
    )

    await getByRole('button', { name: 'Open Face Import' }).click()

    // Upload ZIP file
    const input = document.querySelector(
      'input[type="file"]'
    ) as HTMLInputElement
    const zipFile = createZipFile('faces.zip')
    const dt = new DataTransfer()
    dt.items.add(zipFile)
    input.files = dt.files
    input.dispatchEvent(new Event('change', { bubbles: true }))

    await vi.waitFor(() => {
      expect(personsApi.createFaceImportJob).toHaveBeenCalledWith(zipFile)
    })
    expect(toast.success).toHaveBeenCalledWith(
      '导入任务已创建，后台开始解压处理'
    )

    // Verify polling/completed display
    await expect
      .element(getByText('导入完成', { exact: true }))
      .toBeInTheDocument()
    await expect.element(getByText('总数')).toBeInTheDocument()
    await expect.element(getByText(/人脸底库批量处理完成/i)).toBeInTheDocument()

    // Download failed report
    const downloadFailedBtn = getByRole('button', {
      name: /下载失败报告 \(CSV\)/i,
    })
    await expect.element(downloadFailedBtn).toBeInTheDocument()
    await downloadFailedBtn.click()
    expect(clickSpy).toHaveBeenCalled()

    // Reset to import another batch
    const resetBtn = getByRole('button', { name: /重新导入/i })
    await expect.element(resetBtn).toBeInTheDocument()
    await resetBtn.click()

    await expect
      .element(getByText(/点击选择或拖拽 ZIP 压缩包/i))
      .toBeInTheDocument()
  })
})
