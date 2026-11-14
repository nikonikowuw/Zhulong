import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { DirectionProvider } from '@/context/direction-provider'
import { SearchProvider } from '@/context/search-provider'
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar'
import { type Camera } from '@/features/cameras/contract'
import { UnassignedCamerasPanel } from './components/sidebar/unassigned-cameras-panel'
import { MapRenameDialog } from './components/toolbar/map-rename-dialog'
import { MapUploadDialog } from './components/toolbar/map-upload-dialog'
import { Spatial } from './index'

let mockSearch: Record<string, unknown> = {}

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    getRouteApi: (id: string) => {
      if (id === '/_authenticated/spatial/') {
        return {
          useSearch: () => mockSearch,
          useNavigate: () => vi.fn(),
        }
      }
      return actual.getRouteApi(id as never)
    },
  }
})

const mockTrajectoryMutateAsync = vi.fn().mockResolvedValue({
  mapId: 'map-test-1',
  targetType: 'person',
  targetKey: 'p-deep',
  nodes: [],
})

const mockSavePositionsMutateAsync = vi.fn().mockResolvedValue({
  mapId: 'map-test-1',
  count: 2,
})

const mockUpdateMapNameMutateAsync = vi.fn().mockResolvedValue({
  id: 'map-test-1',
  name: 'Renamed',
})

vi.mock('./hooks/use-trajectory', () => ({
  useTrajectory: () => ({
    mutateAsync: mockTrajectoryMutateAsync,
    isPending: false,
  }),
}))

vi.mock('@/features/persons/contract', () => ({
  useAllPersons: () => ({
    data: {
      items: [
        {
          id: 'p-deep',
          name: 'Deep Person',
          personId: 'EXT-DEEP',
          faces: [],
        },
      ],
      total: 1,
    },
    isLoading: false,
  }),
}))

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
      {
        id: 'map-test-2',
        name: 'Zone B Second Floor',
        imageUrl: '/api/v1/spatial/maps/map-test-2/image',
        width: 1920,
        height: 1080,
        createdAt: '2026-10-29T11:00:00Z',
        updatedAt: '2026-10-29T11:00:00Z',
      },
    ],
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  }),
  useUploadMap: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useUpdateMapName: () => ({
    mutateAsync: mockUpdateMapNameMutateAsync,
    isPending: false,
  }),
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
  useSaveCameraPositions: () => ({
    mutateAsync: mockSavePositionsMutateAsync,
    isPending: false,
  }),
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

vi.mock('@/features/live/contract', () => ({
  CameraFrameSurface: () => <div data-testid='mock-camera-surface' />,
}))

vi.mock('@/components/config-drawer', () => ({
  ConfigDrawer: () => <div data-testid='mock-config-drawer' />,
}))

vi.mock('@/components/profile-dropdown', () => ({
  ProfileDropdown: () => <div data-testid='mock-profile-dropdown' />,
}))

function renderWithProviders(ui: React.ReactNode) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <SearchProvider>
        <DirectionProvider>
          <SidebarProvider>
            <SidebarInset>{ui}</SidebarInset>
          </SidebarProvider>
        </DirectionProvider>
      </SearchProvider>
    </QueryClientProvider>
  )
}

describe('Spatial feature component', () => {
  beforeEach(() => {
    mockSearch = {}
    mockTrajectoryMutateAsync.mockClear()
    mockSavePositionsMutateAsync.mockClear()
    mockUpdateMapNameMutateAsync.mockClear()
  })

  it('renders header, map switcher and trajectory search form', async () => {
    const { getByText } = await renderWithProviders(<Spatial />)

    await expect.element(getByText('Zone A Ground Floor')).toBeInTheDocument()
    await expect.element(getByText('查询轨迹')).toBeInTheDocument()
    await expect.element(getByText('标定点位')).toBeInTheDocument()
    await expect.element(getByText('车辆')).toBeInTheDocument()
    await expect.element(getByText('人员')).toBeInTheDocument()
  })

  it('rejects an empty vehicle plate instead of querying', async () => {
    const { getByRole, getByText } = await renderWithProviders(<Spatial />)

    await getByRole('button', { name: '查询轨迹' }).click()
    await expect.element(getByText('请输入车牌号')).toBeInTheDocument()
  })

  it('switches to calibration mode and keeps the base map visible alongside the cameras panel', async () => {
    const { getByRole, getByText, getByAltText } = await renderWithProviders(
      <Spatial />
    )

    await getByRole('tab', { name: '标定点位' }).click()
    await expect.element(getByText('待标定摄像机')).toBeInTheDocument()
    await expect
      .element(getByAltText('Zone A Ground Floor'))
      .toBeInTheDocument()
  })

  it('supports stamping a camera, preventing stamp-through on existing markers, discard confirmation, and saving positions', async () => {
    const { container, getByRole, getByText } = await renderWithProviders(
      <Spatial />
    )

    await getByRole('tab', { name: '标定点位' }).click()

    // 选中未标定摄像机进入准星模式
    await getByRole('button', { name: '选中' }).click()
    await expect.element(getByText(/准星已就绪：Lobby Central/)).toBeVisible()

    // 在准星模式下点击已有摄像机点位，应打开浮窗而不误落新点
    await getByRole('button', { name: 'Gate East' }).click()
    await expect.element(getByText('实时画面验真')).toBeInTheDocument()
    await expect.element(getByText(/准星已就绪：Lobby Central/)).toBeVisible()

    // 点击底图画布空白处完成落点
    const canvasContent = container.querySelector(
      '[data-spatial-canvas-content]'
    )
    if (!(canvasContent instanceof HTMLElement)) {
      throw new Error('spatial canvas content element not found')
    }
    canvasContent.click()

    await expect
      .element(getByRole('button', { name: 'Lobby Central' }))
      .toBeInTheDocument()

    // 存在未保存修改时切换模式应弹出确认框
    await getByRole('tab', { name: '轨迹检索' }).click()
    await expect.element(getByText('放弃未保存的修改？')).toBeInTheDocument()
    await getByRole('button', { name: '取消' }).click()

    // 保存点位
    await getByRole('button', { name: '保存点位' }).click()
    await expect
      .poll(() => mockSavePositionsMutateAsync)
      .toHaveBeenCalledWith({
        mapId: 'map-test-1',
        positions: expect.arrayContaining([
          expect.objectContaining({ cameraId: 'cam-gate' }),
          expect.objectContaining({ cameraId: 'cam-hall' }),
        ]),
      })
  })

  it('automatically queries trajectory on deep-linked map and syncs person select display', async () => {
    mockSearch = {
      mapId: 'map-test-2',
      targetType: 'person',
      personId: 'p-deep',
    }

    const { getByRole, getByText } = await renderWithProviders(<Spatial />)

    await expect.element(getByText('Zone B Second Floor')).toBeInTheDocument()
    await expect
      .element(
        getByRole('combobox').filter({ hasText: 'Deep Person (EXT-DEEP)' })
      )
      .toBeInTheDocument()

    await expect
      .poll(() => mockTrajectoryMutateAsync)
      .toHaveBeenCalledWith(
        expect.objectContaining({
          mapId: 'map-test-2',
          targetType: 'person',
          personId: 'p-deep',
        })
      )
  })
})

describe('UnassignedCamerasPanel empty states', () => {
  it('distinguishes empty camera inventory from search filter no-match and supports clearing filter', async () => {
    const emptyView = await renderWithProviders(
      <UnassignedCamerasPanel
        cameras={[]}
        positions={[]}
        selectedCameraId={null}
        onSelectCamera={vi.fn()}
        onRemovePosition={vi.fn()}
      />
    )
    await expect
      .element(
        emptyView.getByText('暂无可用摄像机，请先在摄像机管理中添加设备')
      )
      .toBeInTheDocument()
    await emptyView.unmount()

    const cameras: Camera[] = [
      {
        id: 'cam-1',
        name: 'East Gate',
        enabled: true,
        revision: 1,
        health: 'online',
        session: 'running',
        degraded: false,
        stale: false,
        reason: '',
        streams: [],
        createdAt: '2026-10-29T10:00:00Z',
        updatedAt: '2026-10-29T10:00:00Z',
      },
    ]
    const filterView = await renderWithProviders(
      <UnassignedCamerasPanel
        cameras={cameras}
        positions={[]}
        selectedCameraId={null}
        onSelectCamera={vi.fn()}
        onRemovePosition={vi.fn()}
      />
    )

    const searchInput = filterView.getByPlaceholder('搜索摄像机...')
    await searchInput.fill('nonexistent')
    await expect
      .element(filterView.getByText('未找到匹配的摄像机'))
      .toBeInTheDocument()

    await filterView.getByRole('button', { name: '清除筛选' }).click()
    await expect.element(filterView.getByText('East Gate')).toBeInTheDocument()
  })
})

describe('Map dialogs', () => {
  it('rejects non-image files on drop in MapUploadDialog', async () => {
    const { getByRole, getByText } = await renderWithProviders(
      <MapUploadDialog open onOpenChange={vi.fn()} onSuccess={vi.fn()} />
    )

    const dropzone = getByRole('button', {
      name: /点击或拖拽图片文件到此处/,
    })
    const invalidFile = new File(['hello'], 'notes.txt', {
      type: 'text/plain',
    })
    const dt = new DataTransfer()
    dt.items.add(invalidFile)

    const el = dropzone.element()
    el.dispatchEvent(
      new DragEvent('drop', {
        bubbles: true,
        cancelable: true,
        dataTransfer: dt,
      })
    )

    await expect
      .element(getByText('仅支持 PNG、JPG、WebP 或 SVG 图片格式'))
      .toBeInTheDocument()
  })

  it('resets dirty input when MapRenameDialog closes and reopens for the same map', async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const Wrapper = ({ open }: { open: boolean }) => (
      <QueryClientProvider client={queryClient}>
        <MapRenameDialog
          mapId='map-test-1'
          currentName='Original Name'
          open={open}
          onOpenChange={vi.fn()}
        />
      </QueryClientProvider>
    )

    const view = await render(<Wrapper open />)
    const input = view.getByPlaceholder('例如：1号园区主平面图')
    await input.fill('Dirty Draft')
    await expect.element(input).toHaveValue('Dirty Draft')

    await view.rerender(<Wrapper open={false} />)
    await view.rerender(<Wrapper open />)

    await expect
      .element(view.getByPlaceholder('例如：1号园区主平面图'))
      .toHaveValue('Original Name')
  })
})
