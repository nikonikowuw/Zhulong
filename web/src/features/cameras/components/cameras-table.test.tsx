import { useState } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { type ColumnFiltersState } from '@tanstack/react-table'
import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { type Camera } from '../data/schema'
import { CamerasProvider } from './cameras-provider'
import { CamerasTable } from './cameras-table'

vi.mock('@/hooks/use-table-url-state', () => ({
  useTableUrlState: () => {
    const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([])
    return {
      pagination: { pageIndex: 0, pageSize: 10 },
      onPaginationChange: vi.fn(),
      ensurePageInRange: vi.fn(),
      globalFilter: '',
      onGlobalFilterChange: vi.fn(),
      columnFilters,
      onColumnFiltersChange: setColumnFilters,
    }
  },
}))

function buildCamera(
  overrides: Partial<Camera> & Pick<Camera, 'id' | 'name'>
): Camera {
  return {
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
    ...overrides,
  }
}

const mockCameras: Camera[] = [
  buildCamera({
    id: 'cam_test_01',
    name: 'Front Gate Camera',
    health: 'online',
  }),
  buildCamera({
    id: 'cam_test_02',
    name: 'Back Yard Camera',
    health: 'offline',
  }),
  buildCamera({ id: 'cam_test_03', name: 'Warehouse Camera', health: 'error' }),
]

describe('CamerasTable', () => {
  it('renders table, camera rows, and masks RTSP credentials', async () => {
    const queryClient = new QueryClient()

    const { getByText, getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <CamerasProvider>
          <CamerasTable
            data={mockCameras.slice(0, 1)}
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
    await expect
      .element(getByText('rtsp://admin:••••••••@192.168.1.100:554/live/ch0'))
      .toBeInTheDocument()
  })

  it('keeps row selection tied to camera id when sorting changes row order', async () => {
    const queryClient = new QueryClient()

    const { getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <CamerasProvider>
          <CamerasTable
            data={mockCameras}
            isLoading={false}
            isError={false}
            onRetry={vi.fn()}
            search={{}}
            navigate={vi.fn()}
          />
        </CamerasProvider>
      </QueryClientProvider>
    )

    const frontCheckbox = getByRole('checkbox', {
      name: '选择摄像机 Front Gate Camera',
    })
    await userEvent.click(frontCheckbox)
    await expect.element(frontCheckbox).toBeChecked()

    // Sort by camera name descending (Front Gate Camera moves down)
    await userEvent.click(getByRole('button', { name: '摄像机名称' }))
    await userEvent.click(getByRole('menuitem', { name: 'Desc' }))

    await expect
      .element(getByRole('checkbox', { name: '选择摄像机 Front Gate Camera' }))
      .toBeChecked()
    await expect
      .element(getByRole('checkbox', { name: '选择摄像机 Back Yard Camera' }))
      .not.toBeChecked()
  })

  it('supports multi-select health status filtering', async () => {
    const queryClient = new QueryClient()

    const { getByRole, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <CamerasProvider>
          <CamerasTable
            data={mockCameras}
            isLoading={false}
            isError={false}
            onRetry={vi.fn()}
            search={{}}
            navigate={vi.fn()}
          />
        </CamerasProvider>
      </QueryClientProvider>
    )

    await userEvent.click(getByRole('button', { name: '健康状态' }))
    await userEvent.click(getByRole('option', { name: /在线/ }))
    await userEvent.click(getByRole('option', { name: /离线/ }))

    await expect.element(getByText('Front Gate Camera')).toBeInTheDocument()
    await expect.element(getByText('Back Yard Camera')).toBeInTheDocument()
    await expect.element(getByText('Warehouse Camera')).not.toBeInTheDocument()
  })
})
