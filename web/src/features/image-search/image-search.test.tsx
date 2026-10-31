import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { DirectionProvider } from '@/context/direction-provider'
import { LayoutProvider } from '@/context/layout-provider'
import { SearchProvider } from '@/context/search-provider'
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar'
import { imageSearchApi } from './api/image-search-api'
import { ImageSearch } from './index'

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

  it('renders page header, disclaimer, and idle prompt initially', async () => {
    const { getByRole, getByText } = await renderWithProviders(<ImageSearch />)

    await expect
      .element(getByRole('heading', { name: /以图搜图|Visual Search/ }))
      .toBeInTheDocument()
    await expect
      .element(
        getByText(
          /候选结果按相似度排序，不构成身份确认|Candidates are ranked by similarity/
        )
      )
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
})
