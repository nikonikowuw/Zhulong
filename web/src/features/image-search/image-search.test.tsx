import {
  MutationCache,
  QueryClient,
  QueryClientProvider,
} from '@tanstack/react-query'
import { toast } from 'sonner'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { DirectionProvider } from '@/context/direction-provider'
import { LayoutProvider } from '@/context/layout-provider'
import { SearchProvider } from '@/context/search-provider'
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar'
import { imageSearchApi } from './api/image-search-api'
import { ImageSearch } from './index'

vi.mock('sonner', () => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}))

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useLocation: () => ({ href: '/image-search', pathname: '/image-search' }),
  }
})

async function renderWithProviders(ui: React.ReactNode) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
    mutationCache: new MutationCache({
      onError: (error, _variables, _context, mutation) => {
        if (mutation.meta?.suppressGlobalToast) {
          return
        }
        toast.error(error.message)
      },
    }),
  })
  return await render(
    <QueryClientProvider client={queryClient}>
      <SearchProvider>
        <LayoutProvider>
          <DirectionProvider>
            <SidebarProvider>
              <SidebarInset>{ui}</SidebarInset>
            </SidebarProvider>
          </DirectionProvider>
        </LayoutProvider>
      </SearchProvider>
    </QueryClientProvider>
  )
}

function selectTestFile(
  container: Element,
  name = 'test.png',
  type = 'image/png'
) {
  const file = new File(['fake-png-content'], name, { type })
  const input = container.querySelector(
    'input[type="file"]'
  ) as HTMLInputElement
  expect(input).not.toBeNull()
  Object.defineProperty(input, 'files', {
    value: [file],
    configurable: true,
  })
  input.dispatchEvent(new Event('change', { bubbles: true }))
  return file
}

describe('ImageSearch Workbench', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders page header and idle prompt initially', async () => {
    const { getByRole, getByText } = await renderWithProviders(<ImageSearch />)

    await expect
      .element(getByRole('heading', { name: /以图搜图|Visual Search/ }))
      .toBeInTheDocument()
    await expect
      .element(getByText(/拖拽人脸图片至此|Drag & drop an image here/))
      .toBeInTheDocument()
    await expect.element(getByText(/就绪|Ready to Search/)).toBeInTheDocument()
  })

  it('handles search submission, renders disclaimer & match card, and keeps applied criteria stable while editing draft controls', async () => {
    const mockMatches = {
      matches: [
        {
          personId: 'p-target',
          personExternalId: 'EMP-999',
          personName: 'Target Person',
          matchedFaceId: 'f-target',
          similarity: 0.885,
        },
      ],
    }

    vi.spyOn(imageSearchApi, 'searchByImage').mockResolvedValueOnce(mockMatches)

    const { getByRole, getByText, container } = await renderWithProviders(
      <ImageSearch />
    )

    selectTestFile(container)

    const searchBtn = getByRole('button', {
      name: /开始检索|Search Gallery/,
    })
    await expect.element(searchBtn).toBeEnabled()
    await searchBtn.click()

    await expect.poll(() => imageSearchApi.searchByImage).toHaveBeenCalled()

    // Verify card, disclaimer, and applied criteria snapshot rendered
    await expect.element(getByText('Target Person')).toBeInTheDocument()
    await expect.element(getByText('EMP-999')).toBeInTheDocument()
    await expect.element(getByText('88.5%')).toBeInTheDocument()
    await expect
      .element(
        getByText(/不构成身份确认|do not constitute identity confirmation/)
      )
      .toBeInTheDocument()
    await expect
      .element(
        getByText(/阈值 ≥ 70% • 上限 10 人|Threshold ≥ 70% • Top 10 Max/)
      )
      .toBeInTheDocument()

    // Editing draft controls before clicking Search must NOT mutate displayed result criteria
    await getByRole('button', { name: '85%' }).click()
    await getByRole('radio', { name: '50' }).click()
    await expect
      .element(
        getByText(/阈值 ≥ 70% • 上限 10 人|Threshold ≥ 70% • Top 10 Max/)
      )
      .toBeInTheDocument()

    // Verify cross-feature deep-link actions
    await getByRole('button', { name: /抓拍记录|Records/ }).click()
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/records',
      search: expect.objectContaining({ personId: 'p-target' }),
    })

    await getByRole('button', { name: /时空轨迹|Trajectory/ }).click()
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/spatial',
      search: { targetType: 'person', personId: 'p-target' },
    })

    await getByRole('button', { name: /人员档案|Profile/ }).click()
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/persons',
      search: { personId: 'p-target' },
    })
  })

  it('lowers threshold to 0.5 immediately on empty-state retry and hides button once threshold <= 0.5', async () => {
    const searchSpy = vi
      .spyOn(imageSearchApi, 'searchByImage')
      .mockResolvedValueOnce({ matches: [] })
      .mockResolvedValueOnce({ matches: [] })

    const { getByRole, getByText, container } = await renderWithProviders(
      <ImageSearch />
    )

    selectTestFile(container)

    await getByRole('button', { name: /开始检索|Search Gallery/ }).click()

    await expect
      .element(getByText(/未找到匹配的人员|No Matching Personnel Found/))
      .toBeInTheDocument()
    expect(searchSpy).toHaveBeenNthCalledWith(
      1,
      expect.objectContaining({ threshold: 0.7, topK: 10 })
    )

    const lowerRetryBtn = getByRole('button', {
      name: /调低阈值至 50%|Lower Threshold to 50%/,
    })
    await lowerRetryBtn.click()

    await vi.waitFor(() => {
      expect(searchSpy).toHaveBeenCalledTimes(2)
    })
    expect(searchSpy).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({ threshold: 0.5, topK: 10 })
    )

    // Once searched at 50% and still empty, the "Lower to 50%" CTA must not reappear
    await expect.element(lowerRetryBtn).not.toBeInTheDocument()
  })

  it('suppresses duplicate global toast on search error and supports inline retry', async () => {
    const searchSpy = vi
      .spyOn(imageSearchApi, 'searchByImage')
      .mockRejectedValueOnce(new Error('人脸特征提取服务异常'))
      .mockResolvedValueOnce({
        matches: [
          {
            personId: 'p-recovered',
            personName: 'Recovered Person',
            matchedFaceId: 'f-rec',
            similarity: 0.9,
          },
        ],
      })

    const { getByRole, getByText, container } = await renderWithProviders(
      <ImageSearch />
    )

    selectTestFile(container)
    await getByRole('button', { name: /开始检索|Search Gallery/ }).click()

    await expect
      .element(getByText(/检索失败|Search Failed/))
      .toBeInTheDocument()
    await expect.element(getByText('人脸特征提取服务异常')).toBeInTheDocument()
    expect(toast.error).not.toHaveBeenCalled()

    // Click inline retry button
    const retryBtn = getByRole('button', { name: /重新检索|Retry Search/ })
    await retryBtn.click()

    await expect.element(getByText('Recovered Person')).toBeInTheDocument()
    expect(searchSpy).toHaveBeenCalledTimes(2)
  })

  it('resets previous search results when file is cleared', async () => {
    vi.spyOn(imageSearchApi, 'searchByImage').mockResolvedValueOnce({
      matches: [
        {
          personId: 'p-1',
          personName: 'Alice',
          matchedFaceId: 'f-1',
          similarity: 0.95,
        },
      ],
    })

    const { getByRole, getByText, container } = await renderWithProviders(
      <ImageSearch />
    )

    selectTestFile(container)
    await getByRole('button', { name: /开始检索|Search Gallery/ }).click()
    await expect.element(getByText('Alice')).toBeInTheDocument()

    await getByRole('button', { name: /清除|Clear/ }).click()

    await expect.element(getByText(/就绪|Ready to Search/)).toBeInTheDocument()
    await expect.element(getByText('Alice')).not.toBeInTheDocument()
  })

  it('rejects unsupported file formats and files exceeding 10MB', async () => {
    const { container } = await renderWithProviders(<ImageSearch />)
    const input = container.querySelector(
      'input[type="file"]'
    ) as HTMLInputElement

    const badFile = new File(['bad'], 'test.pdf', { type: 'application/pdf' })
    Object.defineProperty(input, 'files', {
      value: [badFile],
      configurable: true,
    })
    input.dispatchEvent(new Event('change', { bubbles: true }))
    expect(toast.error).toHaveBeenCalledWith(
      expect.stringMatching(/不支持的文件格式|Unsupported file format/)
    )

    const bigFile = new File(['a'], 'huge.jpg', { type: 'image/jpeg' })
    Object.defineProperty(bigFile, 'size', {
      value: 11 * 1024 * 1024,
      configurable: true,
    })
    Object.defineProperty(input, 'files', {
      value: [bigFile],
      configurable: true,
    })
    input.dispatchEvent(new Event('change', { bubbles: true }))
    expect(toast.error).toHaveBeenCalledWith(
      expect.stringMatching(/超过 10MB 限制|exceeds 10MB limit/)
    )
  })

  it('renders fallback error description when error message is empty', async () => {
    vi.spyOn(imageSearchApi, 'searchByImage').mockRejectedValueOnce(
      new Error('')
    )

    const { getByRole, getByText, container } = await renderWithProviders(
      <ImageSearch />
    )

    selectTestFile(container)
    await getByRole('button', { name: /开始检索|Search Gallery/ }).click()

    await expect
      .element(getByText(/检索失败|Search Failed/))
      .toBeInTheDocument()
    await expect
      .element(
        getByText(
          /检索服务发生异常，请检查网络或稍后重试|Image search encountered an error/
        )
      )
      .toBeInTheDocument()
  })

  it('supports arrow-key roving focus on Top-K radio group and resets controls to defaults', async () => {
    const { getByRole, getByText, container } = await renderWithProviders(
      <ImageSearch />
    )

    selectTestFile(container)

    const preset85Btn = getByRole('button', { name: '85%' })
    await preset85Btn.click()
    expect(preset85Btn.element().getAttribute('aria-pressed')).toBe('true')

    const top10Btn = getByRole('radio', { name: '10' })
    const top20Btn = getByRole('radio', { name: '20' })
    top10Btn.element().focus()
    top10Btn
      .element()
      .dispatchEvent(
        new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true })
      )
    await vi.waitFor(() => {
      expect(top20Btn.element().getAttribute('aria-checked')).toBe('true')
    })

    await getByRole('button', { name: /重置|Reset/ }).click()

    const preset70Btn = getByRole('button', { name: '70%' })
    expect(preset70Btn.element().getAttribute('aria-pressed')).toBe('true')
    expect(preset85Btn.element().getAttribute('aria-pressed')).toBe('false')
    expect(top10Btn.element().getAttribute('aria-checked')).toBe('true')
    expect(top20Btn.element().getAttribute('aria-checked')).toBe('false')

    await expect.element(getByText(/就绪|Ready to Search/)).toBeInTheDocument()
  })

  it('switches between visual grid view and compact list view with real DOM transitions', async () => {
    vi.spyOn(imageSearchApi, 'searchByImage').mockResolvedValueOnce({
      matches: [
        {
          personId: 'p-1',
          personName: 'Person In View',
          matchedFaceId: 'f-1',
          similarity: 0.91,
        },
      ],
    })

    const { getByRole, getByTitle, getByTestId, container } =
      await renderWithProviders(<ImageSearch />)

    selectTestFile(container)
    await getByRole('button', { name: /开始检索|Search Gallery/ }).click()

    await expect
      .element(getByTestId('image-search-match-card'))
      .toBeInTheDocument()
    expect(
      container.querySelector('[data-testid="image-search-match-row"]')
    ).toBeNull()

    // Switch to list view and verify row renders instead of card
    const listViewBtn = getByTitle(/列表视图|List View/)
    await listViewBtn.click()
    await expect
      .element(getByTestId('image-search-match-row'))
      .toBeInTheDocument()
    expect(
      container.querySelector('[data-testid="image-search-match-card"]')
    ).toBeNull()

    // Switch back to grid view and verify card returns
    const gridViewBtn = getByTitle(/网格|Grid View/)
    await gridViewBtn.click()
    await expect
      .element(getByTestId('image-search-match-card'))
      .toBeInTheDocument()
    expect(
      container.querySelector('[data-testid="image-search-match-row"]')
    ).toBeNull()
  })

  it('opens 1:1 comparison dialog, blocks background clipboard paste while open, and closes cleanly', async () => {
    vi.spyOn(imageSearchApi, 'searchByImage').mockResolvedValueOnce({
      matches: [
        {
          personId: 'p-inspect',
          personExternalId: 'EMP-INSPECT',
          personName: 'Inspect Target',
          matchedFaceId: 'f-inspect',
          similarity: 0.925,
        },
      ],
    })

    const { getByRole, getByText, container } = await renderWithProviders(
      <ImageSearch />
    )

    selectTestFile(container, 'initial.png')
    await getByRole('button', { name: /开始检索|Search Gallery/ }).click()

    await getByText('Inspect Target').click()

    const dialogHeading = getByRole('heading', {
      name: /人脸比对核验|Face Verification & Inspection/,
    })
    await expect.element(dialogHeading).toBeInTheDocument()
    await expect.element(getByText('VS')).toBeInTheDocument()
    await expect
      .element(
        getByText(/512 维特征向量余弦相似度匹配|512-d Vector Cosine Similarity/)
      )
      .toBeInTheDocument()

    // Pasting while dialog is open must be ignored so query image & match never desync
    const pastedFile = new File(['intruder'], 'intruder.png', {
      type: 'image/png',
    })
    const pasteEvent = new Event('paste', {
      bubbles: true,
    }) as unknown as ClipboardEvent
    Object.defineProperty(pasteEvent, 'clipboardData', {
      value: {
        items: [{ type: 'image/png', getAsFile: () => pastedFile }],
      },
    })
    window.dispatchEvent(pasteEvent)

    await expect.element(dialogHeading).toBeInTheDocument()
    await expect
      .element(
        getByText(/已选择文件：initial\.png|Selected file: initial\.png/)
      )
      .toBeInTheDocument()

    // Close dialog using localized footer button
    const closeBtn = getByRole('button', { name: /^(关闭|關閉|Close)$/ })
    await closeBtn.first().click()
    await expect.element(dialogHeading).not.toBeInTheDocument()
  })

  it('handles image pasting from clipboard when idle', async () => {
    const { getByText } = await renderWithProviders(<ImageSearch />)

    const pasteFile = new File(['fake-pasted-png'], 'clipboard.png', {
      type: 'image/png',
    })

    const pasteEvent = new Event('paste', {
      bubbles: true,
    }) as unknown as ClipboardEvent
    Object.defineProperty(pasteEvent, 'clipboardData', {
      value: {
        items: [
          {
            type: 'image/png',
            getAsFile: () => pasteFile,
          },
        ],
      },
    })

    window.dispatchEvent(pasteEvent)

    await expect
      .element(
        getByText(/已选择文件：clipboard\.png|Selected file: clipboard\.png/)
      )
      .toBeInTheDocument()
  })
})
