import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { type Camera } from '../data/schema'
import { CamerasProvider } from './cameras-provider'
import { CamerasTable } from './cameras-table'

vi.mock('@/hooks/use-table-url-state', () => ({
  useTableUrlState: () => ({
    pagination: { pageIndex: 0, pageSize: 10 },
    onPaginationChange: vi.fn(),
    ensurePageInRange: vi.fn(),
    globalFilter: '',
    onGlobalFilterChange: vi.fn(),
    columnFilters: [],
    onColumnFiltersChange: vi.fn(),
  }),
}))

const mockCameras: Camera[] = [
  {
    id: 'cam_test_01',
    name: 'Front Gate Camera',
    enabled: true,
    revision: 1,
    health: 'online',
    session: 'idle',
    degraded: false,
    stale: false,
    reason: '',
    lastCheckedAt: null,
    lastSuccessAt: null,
    streams: [
      {
        id: 1,
        role: 'main',
        protocol: 'rtsp',
        rtspUrl: 'rtsp://admin:passwd123@192.168.1.100:554/live/ch0',
        transport: 'tcp',
        codec: 'h264',
        width: 1920,
        height: 1080,
        fps: 25,
        fpsString: '25 fps',
      },
    ],
    createdAt: '2026-10-09T08:00:00Z',
    updatedAt: '2026-10-09T08:00:00Z',
  },
]

describe('CamerasTable', () => {
  it('renders table and camera data rows', async () => {
    const queryClient = new QueryClient()

    const { getByText, getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <CamerasProvider>
          <CamerasTable
            data={mockCameras}
            total={1}
            isLoading={false}
            isError={false}
            onRetry={vi.fn()}
            search={{}}
            navigate={vi.fn()}
          />
        </CamerasProvider>
      </QueryClientProvider>
    )

    await expect.element(getByRole('table')).toBeInTheDocument()
    await expect.element(getByText('Front Gate Camera')).toBeInTheDocument()
    await expect.element(getByText('cam_test_01')).toBeInTheDocument()
  })
})
