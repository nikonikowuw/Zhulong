import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ConfigDrawer } from '@/components/config-drawer'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { LanguageSwitch } from '@/components/language-switch'
import { Header } from '@/components/layout/header'
import { Main } from '@/components/layout/main'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Search } from '@/components/search'
import { ThemeSwitch } from '@/components/theme-switch'
import { LiveCameraSidebar } from './components/live-camera-sidebar'
import { LiveGrid } from './components/live-grid'
import { LiveToolbar } from './components/live-toolbar'
import { useLiveCameras } from './hooks/use-live-cameras'
import { useLiveGrid } from './hooks/use-live-grid'
import { useLiveShortcuts } from './hooks/use-live-shortcuts'

export function Live(): React.JSX.Element {
  const { t } = useTranslation('live')
  const [isSidebarCollapsed, setIsSidebarCollapsed] = useState(false)
  const [isClearConfirmOpen, setIsClearConfirmOpen] = useState(false)

  const { data: cameras = [], isLoading, error, refetch } = useLiveCameras()

  const {
    layout,
    setLayout,
    activeCellId,
    setActiveCellId,
    maximizedCellId,
    toggleMaximizeCell,
    resetMaximize,
    cells,
    assignCamera,
    clearCell,
    clearAllCells,
    setCellMute,
    setCellStreamType,
    muteAll,
  } = useLiveGrid()

  // 绑定键盘快捷键守卫 (1, 4, 9, Esc)
  useLiveShortcuts({
    onLayoutChange: setLayout,
    onResetMaximize: resetMaximize,
  })

  // 统计当前分配了摄像机的活跃单元格数量
  const activeStreamsCount = cells
    .slice(0, layout)
    .filter((c) => Boolean(c.cameraId)).length

  return (
    <>
      {/* ===== 统一标准 Header (对齐 shadcn-admin) ===== */}
      <Header fixed>
        <Search className='me-auto' />
        <LanguageSwitch />
        <ThemeSwitch />
        <ConfigDrawer />
        <ProfileDropdown />
      </Header>

      {/* ===== 主体监控工作区 ===== */}
      <Main
        fixed
        className='flex flex-1 flex-col gap-2.5 overflow-hidden p-2 sm:p-3 md:p-4'
      >
        {/* 标题栏与多画面工具条 */}
        <div className='flex flex-wrap items-center justify-between gap-2 sm:gap-4'>
          <div>
            <h2 className='text-xl font-bold tracking-tight sm:text-2xl'>
              {t('title')}
            </h2>
            <p className='text-xs text-muted-foreground'>{t('description')}</p>
          </div>
          <LiveToolbar
            layout={layout}
            onLayoutChange={setLayout}
            onMuteAll={muteAll}
            onClearAll={() => setIsClearConfirmOpen(true)}
            activeStreamsCount={activeStreamsCount}
          />
        </div>

        {/* 监控大盘工作区两栏容器 (Master-Detail 现代风格) */}
        <div className='flex min-h-0 flex-1 gap-2.5 overflow-hidden sm:gap-4'>
          <LiveCameraSidebar
            cameras={cameras}
            isLoading={isLoading}
            error={error}
            onRetry={() => void refetch()}
            activeCellId={activeCellId}
            onAssignCamera={(camera, streamType) => {
              assignCamera(activeCellId, camera, streamType)
            }}
            isCollapsed={isSidebarCollapsed}
            onToggleCollapse={() => setIsSidebarCollapsed(!isSidebarCollapsed)}
          />

          <div className='flex flex-1 flex-col overflow-hidden rounded-xl border border-border bg-card/40 p-1.5 shadow-xs sm:p-2'>
            <LiveGrid
              layout={layout}
              activeCellId={activeCellId}
              maximizedCellId={maximizedCellId}
              cells={cells}
              onSelectCell={setActiveCellId}
              onClearCell={clearCell}
              onToggleMaximizeCell={toggleMaximizeCell}
              onSetCellMute={setCellMute}
              onSetCellStreamType={setCellStreamType}
              onDropCameraToCell={(cellId, cam) => {
                assignCamera(cellId, cam, 'main')
                setActiveCellId(cellId)
              }}
            />
          </div>
        </div>
      </Main>

      {/* 清空全部画面安全确认对话框 */}
      <ConfirmDialog
        open={isClearConfirmOpen}
        onOpenChange={setIsClearConfirmOpen}
        title={t('clearConfirmTitle')}
        desc={t('clearConfirmDesc')}
        confirmText={t('clearAll')}
        destructive
        handleConfirm={clearAllCells}
      />
    </>
  )
}

export { Live as LivePage }
export default Live
