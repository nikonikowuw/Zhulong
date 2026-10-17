import { useCallback, useState } from 'react'
import { getRouteApi } from '@tanstack/react-router'
import { LayoutGrid, Plus, Table as TableIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { type NavigateFn } from '@/hooks/use-table-url-state'
import { Button } from '@/components/ui/button'
import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitch } from '@/components/language-switch'
import { Header } from '@/components/layout/header'
import { Main } from '@/components/layout/main'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Search } from '@/components/search'
import { ThemeSwitch } from '@/components/theme-switch'
import { useAlgorithms } from '@/features/algorithms/hooks/use-algorithms'
import { useCameras } from '@/features/cameras/hooks/use-cameras'
import { ChannelTaskDialog } from './components/channel-task-dialog'
import { ChannelTasksView } from './components/channel-tasks-view'
import { TaskDeleteDialog } from './components/task-delete-dialog'
import { TasksComputeBudgetCard } from './components/tasks-compute-budget-card'
import { TasksTable } from './components/tasks-table'
import { type Task } from './data/schema'
import { useChannelTasks } from './hooks/use-channel-tasks'
import { useTaskComputeBudget } from './hooks/use-task-compute-budget'
import { useTasks } from './hooks/use-tasks'

const route = getRouteApi('/_authenticated/tasks/')

export function Tasks(): React.JSX.Element {
  const { t } = useTranslation('tasks')

  // 支持在无 Router 上下文（如独立隔离的单元测试环境）中安全降级
  let search: Record<string, unknown> = {}
  let navigate: NavigateFn = useCallback(() => {}, [])
  try {
    search = route.useSearch() as Record<string, unknown>
    navigate = route.useNavigate() as unknown as NavigateFn
  } catch {
    // 降级为本地状态
  }

  const [localViewMode, setLocalViewMode] = useState<'channel' | 'table'>(
    'channel'
  )
  const viewMode = (search.view as 'channel' | 'table') || localViewMode

  const handleViewModeChange = (mode: 'channel' | 'table') => {
    setLocalViewMode(mode)
    navigate({
      search: (prev) => ({
        ...(prev as Record<string, unknown>),
        view: mode === 'channel' ? undefined : mode,
      }),
    })
  }

  const [configDialogOpen, setConfigDialogOpen] = useState(false)
  const [selectedCameraId, setSelectedCameraId] = useState<string | undefined>(
    undefined
  )
  const [deletingTask, setDeletingTask] = useState<Task | null>(null)

  // 1. 获取基础领域数据
  const { data: tasks = [], isLoading: isTasksLoading } = useTasks()
  const { data: camerasData, isLoading: isCamerasLoading } = useCameras({
    pageSize: 100,
  })
  const { data: algorithms = [], isLoading: isAlgosLoading } = useAlgorithms()

  const cameras = camerasData?.items ?? []
  const isLoading = isTasksLoading || isCamerasLoading || isAlgosLoading

  // 2. 派生算力统计与通道聚合
  const budget = useTaskComputeBudget(tasks, algorithms)
  const channelGroups = useChannelTasks(cameras, algorithms, tasks)

  const handleOpenConfig = (camId?: string) => {
    setSelectedCameraId(camId)
    setConfigDialogOpen(true)
  }

  const handleEditTask = (task: Task) => {
    setSelectedCameraId(task.cameraId)
    setConfigDialogOpen(true)
  }

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
        {/* 顶部标题、视图切换与主 CTA */}
        <div className='flex flex-wrap items-end justify-between gap-3'>
          <div>
            <h2 className='text-2xl font-bold tracking-tight'>
              {t('pageTitle', '视频分析任务')}
            </h2>
            <p className='text-sm text-muted-foreground'>
              {t(
                'pageDesc',
                '管理边缘摄像头与算法包的动态业务调度，支持通道级多算法聚合配置与全平台 1,000 点算力预算管控。'
              )}
            </p>
          </div>

          <div className='flex items-center gap-2'>
            {/* 商业双视角切换：通道卡片 ⇋ 明细表格 */}
            <div className='flex items-center rounded-lg border border-border bg-muted/40 p-1'>
              <Button
                variant={viewMode === 'channel' ? 'secondary' : 'ghost'}
                size='sm'
                className='h-7 gap-1.5 px-2.5 text-xs'
                onClick={() => handleViewModeChange('channel')}
              >
                <LayoutGrid className='h-3.5 w-3.5' />
                <span>{t('viewChannel', '通道聚合视图')}</span>
              </Button>
              <Button
                variant={viewMode === 'table' ? 'secondary' : 'ghost'}
                size='sm'
                className='h-7 gap-1.5 px-2.5 text-xs'
                onClick={() => handleViewModeChange('table')}
              >
                <TableIcon className='h-3.5 w-3.5' />
                <span>{t('viewTable', '任务明细表格')}</span>
              </Button>
            </div>

            <Button
              className='gap-1.5 shadow-xs'
              onClick={() => handleOpenConfig()}
            >
              <Plus className='h-4 w-4' />
              {t('newChannelTask', '配置通道任务')}
            </Button>
          </div>
        </div>

        {/* 算力配额全局指示看板 */}
        <TasksComputeBudgetCard budget={budget} />

        {/* 主体视图区域 */}
        {viewMode === 'channel' ? (
          <ChannelTasksView
            groups={channelGroups}
            onConfigureCamera={(camId) => handleOpenConfig(camId)}
          />
        ) : (
          <TasksTable
            tasks={tasks}
            cameras={cameras}
            algorithms={algorithms}
            isLoading={isLoading}
            search={search}
            navigate={navigate}
            onEditTask={handleEditTask}
            onDeleteTask={(task) => setDeletingTask(task)}
          />
        )}
      </Main>

      {/* 通道多算法统一配置弹窗 */}
      <ChannelTaskDialog
        open={configDialogOpen}
        onOpenChange={setConfigDialogOpen}
        initialCameraId={selectedCameraId}
        cameras={cameras}
        algorithms={algorithms}
        allTasks={tasks}
      />

      {/* 删除任务二次确认弹窗 */}
      <TaskDeleteDialog
        task={deletingTask}
        open={Boolean(deletingTask)}
        onOpenChange={(open) => !open && setDeletingTask(null)}
      />
    </>
  )
}
