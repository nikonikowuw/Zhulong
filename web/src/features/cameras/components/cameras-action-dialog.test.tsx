import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { Button } from '@/components/ui/button'
import { CamerasActionDialog } from './cameras-action-dialog'
import { CamerasProvider, useCamerasContext } from './cameras-provider'

vi.mock('../hooks/use-cameras', () => ({
  useCreateCamera: () => ({
    mutateAsync: vi.fn(),
    isPending: false,
  }),
  useUpdateCamera: () => ({
    mutateAsync: vi.fn(),
    isPending: false,
  }),
}))

function TestComponent() {
  const { setOpen } = useCamerasContext()
  return (
    <div>
      <Button onClick={() => setOpen('create')}>Open Create</Button>
      <CamerasActionDialog />
    </div>
  )
}

describe('CamerasActionDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders create dialog with name and rtsp fields when opened', async () => {
    const queryClient = new QueryClient()

    const { getByRole, getByPlaceholder } = await render(
      <QueryClientProvider client={queryClient}>
        <CamerasProvider>
          <TestComponent />
        </CamerasProvider>
      </QueryClientProvider>
    )

    // 点击按钮打开弹窗
    const openBtn = getByRole('button', { name: /Open Create/i })
    await openBtn.click()

    await expect
      .element(getByRole('heading', { name: /新增摄像机资产/i }))
      .toBeInTheDocument()

    await expect
      .element(getByPlaceholder(/例如：园区主大门 4K 枪机/i))
      .toBeInTheDocument()

    await expect
      .element(getByRole('button', { name: /确认添加/i }))
      .toBeInTheDocument()
  })
})
