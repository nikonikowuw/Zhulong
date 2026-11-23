import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { ListPage } from '@/components/list-page'
import { CamerasDialogs } from './components/cameras-dialogs'
import { CamerasPrimaryButtons } from './components/cameras-primary-buttons'
import { CamerasProvider } from './components/cameras-provider'
import { CamerasTable } from './components/cameras-table'
import { useCameraEvents } from './hooks/use-camera-events'
import { useAllCameras } from './hooks/use-cameras'

const route = getRouteApi('/_authenticated/cameras/')

function CamerasContent() {
  const { t } = useTranslation('cameras')
  const search = route.useSearch()
  const navigate = route.useNavigate()

  const { data, isLoading, isError, refetch } = useAllCameras()

  // 双轨状态: SSE 实时事件订阅 (零轮询原地 patch)
  useCameraEvents(true)

  const cameras = data?.items ?? []

  return (
    <>
      <ListPage
        title={t('title')}
        description={t('description')}
        actions={<CamerasPrimaryButtons />}
      >
        {/* 核心数据表格 (首屏独占高信息密度工作区) */}
        <CamerasTable
          data={cameras}
          isLoading={isLoading}
          isError={isError}
          onRetry={() => void refetch()}
          search={search}
          navigate={navigate}
        />
      </ListPage>

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
