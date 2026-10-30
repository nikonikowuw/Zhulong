import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { DirectionProvider } from '@/context/direction-provider'
import { SidebarProvider } from '@/components/ui/sidebar'
import { Spatial } from './index'

vi.mock('./hooks/use-spatial-maps', () => ({
  useSpatialMaps: () => ({
    data: [
      {
        id: 'map-test-1',
        name: 'Zone A Ground Floor',
        imageUrl: '/api/v1/spatial/maps/map-test-1/image',
        width: 1920,
        height: 1080,
        createdAt: '2026-10-29T10:00:00Z',
        updatedAt: '2026-10-29T10:00:00Z',
      },
    ],
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  }),
  useUploadMap: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useUpdateMapName: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useDeleteMap: () => ({ mutateAsync: vi.fn(), isPending: false }),
}))

vi.mock('./hooks/use-camera-positions', () => ({
  useCameraPositions: () => ({
    data: [
      {
        cameraId: 'cam-gate',
        cameraName: 'Gate East',
        x: 0.2,
        y: 0.3,
        angle: 45,
      },
    ],
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  }),
  useSaveCameraPositions: () => ({ mutateAsync: vi.fn(), isPending: false }),
}))

// 跨特性消费一律经门面：mock 的路径必须与被测代码的导入路径一致。
vi.mock('@/features/cameras/contract', () => ({
  useAllCameras: () => ({
    data: {
      items: [
        {
          id: 'cam-gate',
          name: 'Gate East',
          enabled: true,
          health: 'online',
          session: 'running',
          streams: [],
        },
        {
          id: 'cam-hall',
          name: 'Lobby Central',
          enabled: true,
          health: 'online',
          session: 'running',
          streams: [],
        },
      ],
      total: 2,
    },
    isLoading: false,
  }),
}))

vi.mock('@/features/persons/contract', () => ({
  useAllPersons: () => ({ data: { items: [], total: 0 }, isLoading: false }),
}))

vi.mock('@/features/live/contract', () => ({
  CameraFrameSurface: () => <div data-testid='mock-camera-surface' />,
}))

vi.mock('@/components/config-drawer', () => ({
  ConfigDrawer: () => <div data-testid='mock-config-drawer' />,
}))

vi.mock('@/components/profile-dropdown', () => ({
  ProfileDropdown: () => <div data-testid='mock-profile-dropdown' />,
}))

describe('Spatial feature component', () => {
  it('renders header, map switcher and trajectory search form', async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    const { getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <DirectionProvider>
          <SidebarProvider>
            <Spatial />
          </SidebarProvider>
        </DirectionProvider>
      </QueryClientProvider>
    )

    await expect.element(getByText('Zone A Ground Floor')).toBeInTheDocument()
    await expect.element(getByText('查询轨迹')).toBeInTheDocument()
    await expect.element(getByText('标定点位')).toBeInTheDocument()
    await expect.element(getByText('车辆')).toBeInTheDocument()
    await expect.element(getByText('人员')).toBeInTheDocument()
  })

  it('rejects an empty vehicle plate instead of querying', async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    const { getByRole, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <DirectionProvider>
          <SidebarProvider>
            <Spatial />
          </SidebarProvider>
        </DirectionProvider>
      </QueryClientProvider>
    )

    await getByRole('button', { name: '查询轨迹' }).click()
    await expect.element(getByText('请输入车牌号')).toBeInTheDocument()
  })
})
