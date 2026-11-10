import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { toast } from 'sonner'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { Button } from '@/components/ui/button'
import { personsApi } from '../api/persons-api'
import {
  type PersonImportCommitResponse,
  type PersonImportPreviewResponse,
} from '../data/schema'
import { PersonsImportDialog } from './persons-import-dialog'
import { PersonsProvider, usePersonsContext } from './persons-provider'

vi.mock('../api/persons-api', () => ({
  personsApi: {
    previewImport: vi.fn(),
    commitImport: vi.fn(),
  },
}))

vi.mock('sonner', () => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}))

function TestComponent() {
  const { setOpen } = usePersonsContext()
  return (
    <>
      <Button onClick={() => setOpen('import')}>Open Import</Button>
      <PersonsImportDialog />
    </>
  )
}

function createCsvFile(
  name = 'test.csv',
  content = 'name,personId\nAlice,EMP001\n'
) {
  return new File([content], name, { type: 'text/csv' })
}

describe('PersonsImportDialog', () => {
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

  it('opens with the CSV upload prompt and allows downloading template', async () => {
    vi.useFakeTimers()
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

    await getByRole('button', { name: 'Open Import' }).click()

    await expect
      .element(getByRole('heading', { name: /批量导入人员/i }))
      .toBeInTheDocument()
    await expect
      .element(getByRole('button', { name: /下载 CSV 模板/i }))
      .toBeInTheDocument()
    await expect.element(getByText(/CSV 格式示例/i)).toBeInTheDocument()

    // 触发下载模板
    await getByRole('button', { name: /下载 CSV 模板/i }).click()
    expect(createUrlSpy).toHaveBeenCalled()
    expect(clickSpy).toHaveBeenCalled()
    // revoke 必须延后：立即撤销会让 Safari/Firefox 在下载启动前丢掉 Blob。
    expect(revokeUrlSpy).not.toHaveBeenCalled()
    vi.advanceTimersByTime(10_000)
    expect(revokeUrlSpy).toHaveBeenCalledWith('blob:mock-url')
    vi.useRealTimers()
  })

  it('rejects non-csv files with an error toast', async () => {
    const { getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <TestComponent />
        </PersonsProvider>
      </QueryClientProvider>
    )

    await getByRole('button', { name: 'Open Import' }).click()

    const input = document.querySelector(
      'input[type="file"]'
    ) as HTMLInputElement
    const nonCsv = new File(['hello'], 'test.txt', { type: 'text/plain' })
    const dt = new DataTransfer()
    dt.items.add(nonCsv)
    input.files = dt.files
    input.dispatchEvent(new Event('change', { bubbles: true }))

    expect(toast.error).toHaveBeenCalledWith('请选择 CSV 文件')
    expect(personsApi.previewImport).not.toHaveBeenCalled()
  })

  it('handles preview, renders error rows, downloads failed csv, and commits with cache invalidation', async () => {
    const previewData: PersonImportPreviewResponse = {
      total: 2,
      importable: 1,
      failed: 1,
      items: [
        {
          line: 2,
          name: '张三',
          personId: 'EMP-001',
          canImport: true,
          issues: [],
        },
        {
          line: 3,
          name: '',
          personId: 'EMP-001',
          canImport: false,
          issues: [
            {
              field: 'name',
              code: 'NAME_REQUIRED',
            },
          ],
        },
      ],
    }

    const commitData: PersonImportCommitResponse = {
      total: 2,
      imported: 1,
      failed: 1,
      items: [
        {
          line: 2,
          name: '张三',
          personId: 'EMP-001',
          canImport: true,
          imported: true,
          issues: [],
        },
        {
          line: 3,
          name: '',
          personId: 'EMP-001',
          canImport: false,
          imported: false,
          issues: [
            {
              field: 'name',
              code: 'NAME_REQUIRED',
            },
          ],
        },
      ],
    }

    vi.mocked(personsApi.previewImport).mockResolvedValue(previewData)
    vi.mocked(personsApi.commitImport).mockResolvedValue(commitData)

    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
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

    await getByRole('button', { name: 'Open Import' }).click()

    // 1. 上传 CSV 并触发预览
    const input = document.querySelector(
      'input[type="file"]'
    ) as HTMLInputElement
    const csvFile = createCsvFile('persons.csv')
    const dt = new DataTransfer()
    dt.items.add(csvFile)
    input.files = dt.files
    input.dispatchEvent(new Event('change', { bubbles: true }))

    // 验证预览数据及统计
    await expect.element(getByText('persons.csv')).toBeInTheDocument()
    await expect.element(getByText('总行数')).toBeInTheDocument()
    await expect
      .element(getByRole('cell', { name: '张三' }))
      .toBeInTheDocument()
    await expect
      .element(getByRole('cell', { name: '可导入' }))
      .toBeInTheDocument()
    await expect
      .element(getByRole('cell', { name: '需修正' }))
      .toBeInTheDocument()
    await expect
      .element(getByRole('cell', { name: '姓名不能为空' }))
      .toBeInTheDocument()

    // 2. 验证下载失败行
    const downloadFailedBtn = getByRole('button', { name: /下载失败行/i })
    await expect.element(downloadFailedBtn).toBeInTheDocument()
    await downloadFailedBtn.click()
    expect(createUrlSpy).toHaveBeenCalled()
    expect(clickSpy).toHaveBeenCalled()
    // revoke 延迟 10s（共享的 triggerDownload），此处只断言"已安排"。
    expect(revokeUrlSpy).not.toHaveBeenCalled()

    // 3. 确认导入提交
    const confirmBtn = getByRole('button', { name: /确认导入/i })
    await expect.element(confirmBtn).toBeInTheDocument()
    await confirmBtn.click()

    expect(personsApi.commitImport).toHaveBeenCalledWith(csvFile)
    await expect
      .element(getByText(/成功导入 1 条，1 条失败/i))
      .toBeInTheDocument()
    await expect
      .element(getByRole('cell', { name: '已导入' }))
      .toBeInTheDocument()
    expect(toast.success).toHaveBeenCalledWith('导入完成')

    // 4. 验证列表查询缓存失效（刷新）
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['persons'] })
  })

  it('allows resetting and choosing another file', async () => {
    const previewData: PersonImportPreviewResponse = {
      total: 1,
      importable: 1,
      failed: 0,
      items: [
        {
          line: 2,
          name: '李四',
          personId: 'EMP-002',
          canImport: true,
          issues: [],
        },
      ],
    }
    vi.mocked(personsApi.previewImport).mockResolvedValue(previewData)

    const { getByRole, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <TestComponent />
        </PersonsProvider>
      </QueryClientProvider>
    )

    await getByRole('button', { name: 'Open Import' }).click()

    const input = document.querySelector(
      'input[type="file"]'
    ) as HTMLInputElement
    const csvFile = createCsvFile('another.csv')
    const dt = new DataTransfer()
    dt.items.add(csvFile)
    input.files = dt.files
    input.dispatchEvent(new Event('change', { bubbles: true }))

    await expect.element(getByText('another.csv')).toBeInTheDocument()

    // 点击重新选择
    const resetBtn = getByRole('button', { name: /重新选择/i })
    await resetBtn.click()

    // 回到初始选择文件状态
    await expect.element(getByText(/选择 CSV 文件/i)).toBeInTheDocument()
    await expect
      .element(getByRole('button', { name: /下载 CSV 模板/i }))
      .toBeInTheDocument()
  })
})
