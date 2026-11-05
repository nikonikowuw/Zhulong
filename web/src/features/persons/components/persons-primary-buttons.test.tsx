import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { toast } from 'sonner'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { TooltipProvider } from '@/components/ui/tooltip'
import { personsApi } from '../api/persons-api'
import { PersonsPrimaryButtons } from './persons-primary-buttons'
import { PersonsProvider } from './persons-provider'

vi.mock('../api/persons-api', () => ({
  personsApi: {
    exportManifest: vi.fn(),
  },
}))

vi.mock('sonner', () => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
    info: vi.fn(),
  },
}))

function renderButtons(props: { search?: string; faceStatus?: string } = {}) {
  const queryClient = new QueryClient()
  return render(
    <QueryClientProvider client={queryClient}>
      <TooltipProvider>
        <PersonsProvider>
          <PersonsPrimaryButtons {...props} />
        </PersonsProvider>
      </TooltipProvider>
    </QueryClientProvider>
  )
}

describe('PersonsPrimaryButtons export', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('passes the active search and face-status filters to the export API', async () => {
    vi.mocked(personsApi.exportManifest).mockResolvedValue(undefined)
    const screen = await renderButtons({
      search: 'alice',
      faceStatus: 'missing',
    })

    await screen.getByRole('button', { name: /导出采像清单/i }).click()

    await expect
      .poll(() => vi.mocked(personsApi.exportManifest).mock.calls.length)
      .toBe(1)
    expect(personsApi.exportManifest).toHaveBeenCalledWith({
      search: 'alice',
      faceStatus: 'missing',
    })
    await expect.poll(() => vi.mocked(toast.success).mock.calls.length).toBe(1)
  })

  it('exports the unfiltered list when no filters are active', async () => {
    vi.mocked(personsApi.exportManifest).mockResolvedValue(undefined)
    const screen = await renderButtons()

    await screen.getByRole('button', { name: /导出采像清单/i }).click()

    await expect
      .poll(() => vi.mocked(personsApi.exportManifest).mock.calls.length)
      .toBe(1)
    expect(personsApi.exportManifest).toHaveBeenCalledWith({
      search: undefined,
      faceStatus: undefined,
    })
  })

  it('keeps the button usable and shows no success toast when the export fails', async () => {
    vi.mocked(personsApi.exportManifest).mockRejectedValue(new Error('boom'))
    const screen = await renderButtons({ faceStatus: 'missing' })

    await screen.getByRole('button', { name: /导出采像清单/i }).click()

    await expect
      .poll(() => vi.mocked(personsApi.exportManifest).mock.calls.length)
      .toBe(1)
    expect(toast.success).not.toHaveBeenCalled()
    await expect
      .element(screen.getByRole('button', { name: /导出采像清单/i }))
      .toBeEnabled()
  })
})
