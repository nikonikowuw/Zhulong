import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitch } from '@/components/language-switch'
import { Header } from '@/components/layout/header'
import { Main } from '@/components/layout/main'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Search } from '@/components/search'
import { ThemeSwitch } from '@/components/theme-switch'
import { CamerasDialogs } from './components/cameras-dialogs'
import { CamerasPrimaryButtons } from './components/cameras-primary-buttons'
import { CamerasProvider } from './components/cameras-provider'
import { CamerasTable } from './components/cameras-table'
import { useCameraEvents } from './hooks/use-camera-events'
import { useCameras } from './hooks/use-cameras'

const route = getRouteApi('/_authenticated/cameras/')

function CamerasContent() {
  const { t } = useTranslation('cameras')
  const search = route.useSearch()
  const navigate = route.useNavigate()

  // 双轨状态 1: RESTful 分页查询 (与 URL 参数同步)
  const page = typeof search.page === 'number' ? search.page : 1
  const pageSize = typeof search.pageSize === 'number' ? search.pageSize : 10
  const searchQuery =
    typeof search.search === 'string' ? search.search : undefined

  const { data, isLoading, isError, refetch } = useCameras({
    page,
    pageSize,
    search: searchQuery,
  })

  // 双轨状态 2: SSE 实时事件订阅 (零轮询原地 patch)
  useCameraEvents(true)

  const cameras = data?.items ?? []
  const total = data?.total ?? 0

  return (
    <>
      <Header fixed>
        <Search className='me-auto' />
        <LanguageSwitch />
        <ThemeSwitch />
        <ConfigDrawer />
        <ProfileDropdown />
      </Header>

      <Main className='flex flex-1 flex-col gap-4 sm:gap-6'>
        {/* 页面标题与主操作区 */}
        <div className='flex flex-wrap items-end justify-between gap-2'>
          <div>
            <h2 className='text-2xl font-bold tracking-tight'>{t('title')}</h2>
            <p className='text-sm text-muted-foreground'>{t('description')}</p>
          </div>
          <CamerasPrimaryButtons />
        </div>

        {/* 核心数据表格 (首屏独占高信息密度工作区) */}
        <CamerasTable
          data={cameras}
          total={total}
          isLoading={isLoading}
          isError={isError}
          onRetry={() => void refetch()}
          search={search}
          navigate={navigate}
        />
      </Main>

      {/* 弹窗集中挂载 */}
      <CamerasDialogs />
    </>
  )
}

export function Cameras() {
  return (
    <CamerasProvider>
      <CamerasContent />
    </CamerasProvider>
  )
}
