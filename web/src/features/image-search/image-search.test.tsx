import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
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

  it('handles search submission and renders match card with actions', async () => {
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

    // Simulate selecting file
    const file = new File(['fake-png-content'], 'test.png', {
      type: 'image/png',
    })
    const input = container.querySelector(
      'input[type="file"]'
    ) as HTMLInputElement
    expect(input).not.toBeNull()

    // Trigger file change event
    Object.defineProperty(input, 'files', {
      value: [file],
    })
    input.dispatchEvent(new Event('change', { bubbles: true }))

    // Now Search button should be enabled
    const searchBtn = getByRole('button', {
      name: /开始检索|Search Gallery/,
    })
    await expect.element(searchBtn).toBeEnabled()

    await searchBtn.click()

    // Verify search was invoked
    await expect.poll(() => imageSearchApi.searchByImage).toHaveBeenCalled()

    // Verify card rendered
    await expect.element(getByText('Target Person')).toBeInTheDocument()
    await expect.element(getByText('EMP-999')).toBeInTheDocument()
    await expect.element(getByText('88.5%')).toBeInTheDocument()
    await expect.element(getByText(/^(相似度|Similarity)$/)).toBeInTheDocument()

    // Verify actions
    const recordsBtn = getByRole('button', {
      name: /抓拍记录|Records/,
    })
    await recordsBtn.click()
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/records',
      search: expect.objectContaining({ personId: 'p-target' }),
    })

    const trajBtn = getByRole('button', {
      name: /时空轨迹|Trajectory/,
    })
    await trajBtn.click()
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/spatial',
      search: { targetType: 'person', personId: 'p-target' },
    })

    const profileBtn = getByRole('button', {
      name: /人员档案|Profile/,
    })
    await profileBtn.click()
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/persons',
      search: { personId: 'p-target' },
    })
  })

  it('renders empty state when search returns no matches', async () => {
    vi.spyOn(imageSearchApi, 'searchByImage').mockResolvedValueOnce({
      matches: [],
    })

    const { getByRole, getByText, container } = await renderWithProviders(
      <ImageSearch />
    )

    const file = new File(['fake-png-content'], 'test.png', {
      type: 'image/png',
    })
    const input = container.querySelector(
      'input[type="file"]'
    ) as HTMLInputElement
    expect(input).not.toBeNull()
    Object.defineProperty(input, 'files', {
      value: [file],
    })
    input.dispatchEvent(new Event('change', { bubbles: true }))

    const searchBtn = getByRole('button', {
      name: /开始检索|Search Gallery/,
    })
    await searchBtn.click()

    await expect
      .element(getByText(/未找到匹配的人员|No Matching Personnel Found/))
      .toBeInTheDocument()
  })

  it('renders error message when search fails', async () => {
    vi.spyOn(imageSearchApi, 'searchByImage').mockRejectedValueOnce(
      new Error('人脸特征提取服务异常')
    )

    const { getByRole, getByText, container } = await renderWithProviders(
      <ImageSearch />
    )

    const file = new File(['fake-png-content'], 'test.png', {
      type: 'image/png',
    })
    const input = container.querySelector(
      'input[type="file"]'
    ) as HTMLInputElement
    expect(input).not.toBeNull()
    Object.defineProperty(input, 'files', {
      value: [file],
    })
    input.dispatchEvent(new Event('change', { bubbles: true }))

    const searchBtn = getByRole('button', {
      name: /开始检索|Search Gallery/,
    })
    await searchBtn.click()

    await expect
      .element(getByText(/检索失败|Search Failed/))
      .toBeInTheDocument()
    await expect.element(getByText('人脸特征提取服务异常')).toBeInTheDocument()
  })

  it('resets previous search results when file is cleared', async () => {
    const mockMatches = {
      matches: [
        {
          personId: 'p-1',
          personName: 'Alice',
          matchedFaceId: 'f-1',
          similarity: 0.95,
        },
      ],
    }

    vi.spyOn(imageSearchApi, 'searchByImage').mockResolvedValueOnce(mockMatches)

    const { getByRole, getByText, container } = await renderWithProviders(
      <ImageSearch />
    )

    const file = new File(['fake-png-content'], 'test.png', {
      type: 'image/png',
    })
    const input = container.querySelector(
      'input[type="file"]'
    ) as HTMLInputElement
    Object.defineProperty(input, 'files', { value: [file] })
    input.dispatchEvent(new Event('change', { bubbles: true }))

    const searchBtn = getByRole('button', { name: /开始检索|Search Gallery/ })
    await searchBtn.click()

    await expect.element(getByText('Alice')).toBeInTheDocument()

    // Clear the selected file
    const clearBtn = getByRole('button', { name: /清除|Clear/ })
    await clearBtn.click()

    // Verify it returned to idle state rather than displaying stale matches
    await expect.element(getByText(/就绪|Ready to Search/)).toBeInTheDocument()
    await expect.element(getByText('Alice')).not.toBeInTheDocument()
  })

  it('rejects unsupported file formats and files exceeding 10MB', async () => {
    const { container } = await renderWithProviders(<ImageSearch />)
    const input = container.querySelector(
      'input[type="file"]'
    ) as HTMLInputElement

    // 1. Unsupported extension/MIME
    const badFile = new File(['bad'], 'test.pdf', { type: 'application/pdf' })
    Object.defineProperty(input, 'files', {
      value: [badFile],
      configurable: true,
    })
    input.dispatchEvent(new Event('change', { bubbles: true }))
    expect(toast.error).toHaveBeenCalledWith(
      expect.stringMatching(/不支持的文件格式|Unsupported file format/)
    )

    // 2. File exceeding 10MB
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

    const file = new File(['fake-png-content'], 'test.png', {
      type: 'image/png',
    })
    const input = container.querySelector(
      'input[type="file"]'
    ) as HTMLInputElement
    Object.defineProperty(input, 'files', { value: [file] })
    input.dispatchEvent(new Event('change', { bubbles: true }))

    const searchBtn = getByRole('button', { name: /开始检索|Search Gallery/ })
    await searchBtn.click()

    await expect
      .element(getByText(/检索失败|Search Failed/))
      .toBeInTheDocument()
    await expect
      .element(
        getByText(
          /检索服务发生异常，请检查网络或稍后重试|An error occurred during search/
        )
      )
      .toBeInTheDocument()
  })

  it('resets controls to default threshold and top-k when reset button is clicked', async () => {
    const { getByRole, getByText, container } = await renderWithProviders(
      <ImageSearch />
    )

    const file = new File(['fake-png-content'], 'test.png', {
      type: 'image/png',
    })
    const input = container.querySelector(
      'input[type="file"]'
    ) as HTMLInputElement
    Object.defineProperty(input, 'files', { value: [file] })
    input.dispatchEvent(new Event('change', { bubbles: true }))

    // Change preset threshold to 85%
    const preset85Btn = getByRole('button', { name: '85%' })
    await preset85Btn.click()
    expect(preset85Btn.element().getAttribute('aria-pressed')).toBe('true')

    // Change Top-K to 50
    const top50Btn = getByRole('radio', { name: '50' })
    await top50Btn.click()
    expect(top50Btn.element().getAttribute('aria-checked')).toBe('true')

    // Click controls reset button
    const resetBtn = getByRole('button', { name: /重置|Reset/ })
    await resetBtn.click()

    // Threshold should revert to 70% and Top-K to 10
    const preset70Btn = getByRole('button', { name: '70%' })
    expect(preset70Btn.element().getAttribute('aria-pressed')).toBe('true')
    expect(preset85Btn.element().getAttribute('aria-pressed')).toBe('false')

    const top10Btn = getByRole('radio', { name: '10' })
    expect(top10Btn.element().getAttribute('aria-checked')).toBe('true')
    expect(top50Btn.element().getAttribute('aria-checked')).toBe('false')

    // Dropped image and results should be reset to idle
    await expect.element(getByText(/就绪|Ready to Search/)).toBeInTheDocument()
  })

  it('switches between visual grid view and compact list view', async () => {
    const mockMatches = {
      matches: [
        {
          personId: 'p-1',
          personName: 'Person In View',
          matchedFaceId: 'f-1',
          similarity: 0.91,
        },
      ],
    }

    vi.spyOn(imageSearchApi, 'searchByImage').mockResolvedValueOnce(mockMatches)

    const { getByRole, getByTitle, container } = await renderWithProviders(
      <ImageSearch />
    )

    const file = new File(['fake-png-content'], 'test.png', {
      type: 'image/png',
    })
    const input = container.querySelector(
      'input[type="file"]'
    ) as HTMLInputElement
    Object.defineProperty(input, 'files', { value: [file] })
    input.dispatchEvent(new Event('change', { bubbles: true }))

    const searchBtn = getByRole('button', { name: /开始检索|Search Gallery/ })
    await searchBtn.click()

    // Switch to list view using title
    const listViewBtn = getByTitle(/列表视图|List View/)
    await listViewBtn.click()

    // Switch back to grid view using title
    const gridViewBtn = getByTitle(/网格|Grid View/)
    await gridViewBtn.click()
  })

  it('opens 1:1 side-by-side comparison inspection dialog when card is clicked', async () => {
    const mockMatches = {
      matches: [
        {
          personId: 'p-inspect',
          personExternalId: 'EMP-INSPECT',
          personName: 'Inspect Target',
          matchedFaceId: 'f-inspect',
          similarity: 0.925,
        },
      ],
    }

    vi.spyOn(imageSearchApi, 'searchByImage').mockResolvedValueOnce(mockMatches)

    const { getByRole, getByText, container } = await renderWithProviders(
      <ImageSearch />
    )

    const file = new File(['fake-png-content'], 'test.png', {
      type: 'image/png',
    })
    const input = container.querySelector(
      'input[type="file"]'
    ) as HTMLInputElement
    Object.defineProperty(input, 'files', { value: [file] })
    input.dispatchEvent(new Event('change', { bubbles: true }))

    const searchBtn = getByRole('button', { name: /开始检索|Search Gallery/ })
    await searchBtn.click()

    // Click candidate card to inspect
    const nameElem = getByText('Inspect Target')
    await nameElem.click()

    // Verify comparison dialog opens
    await expect
      .element(
        getByRole('heading', {
          name: /人脸比对核验|Face Verification & Inspection/,
        })
      )
      .toBeInTheDocument()
    await expect.element(getByText('VS')).toBeInTheDocument()
    await expect
      .element(
        getByText(/512 维特征向量余弦相似度匹配|512-d Vector Cosine Similarity/)
      )
      .toBeInTheDocument()

    // Close dialog
    const closeBtn = getByRole('button', { name: '关闭' })
    await closeBtn.click()
  })

  it('handles image pasting from clipboard', async () => {
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
