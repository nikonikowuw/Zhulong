import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { ListPage } from '@/components/list-page'
import { useAlgorithms } from '@/features/algorithms/contract'
import { useAllCameras, useCameraEvents } from '@/features/cameras/contract'
import { TasksComputeBudgetCard } from './components/tasks-compute-budget-card'
import { TasksDialogs } from './components/tasks-dialogs'
import { TasksPrimaryButtons } from './components/tasks-primary-buttons'
import { TasksProvider } from './components/tasks-provider'
import { TasksTable } from './components/tasks-table'
import { useTaskComputeBudget } from './hooks/use-task-compute-budget'
import { useTasks } from './hooks/use-tasks'

const route = getRouteApi('/_authenticated/tasks/')

function TasksContent() {
  const { t } = useTranslation('tasks')
  const search = route.useSearch()
  const navigate = route.useNavigate()

  useCameraEvents(true)

  const {
    data: tasks = [],
    isLoading: isTasksLoading,
    isError: isTasksError,
    refetch: refetchTasks,
  } = useTasks()
  const { data: camerasData, isLoading: isCamerasLoading } = useAllCameras()
  const { data: algorithms = [], isLoading: isAlgosLoading } = useAlgorithms()

  const cameras = camerasData?.items ?? []
  const isLoading = isTasksLoading || isCamerasLoading || isAlgosLoading
  const budget = useTaskComputeBudget(tasks, algorithms)

  // 只有带算法订阅的任务才是用户眼中的「任务」。
  // 后端会落下 algorithms 为空的任务行（通道被清空后残留），
  // 它们不产生明细行、也不占算力，不能因此渲染算力看板。
  const hasConfiguredTasks = tasks.some(
    (task) => (task.algorithms?.length ?? 0) > 0
  )

  return (
    <>
      <ListPage
        title={t('pageTitle', '视频分析任务')}
        description={t(
          'pageDesc',
          '管理边缘摄像头与算法包的动态业务调度，支持通道级多算法聚合配置与全平台 1,000 点算力预算管控。'
        )}
        actions={<TasksPrimaryButtons />}
      >
        {/* 算力配额全局指示看板：无算法订阅时既无占用也无调度，不占位渲染 */}
        {hasConfiguredTasks && <TasksComputeBudgetCard budget={budget} />}

        {/* 核心数据表格 */}
        <TasksTable
          tasks={tasks}
          cameras={cameras}
          algorithms={algorithms}
          isLoading={isLoading}
          isError={isTasksError}
          onRetry={() => void refetchTasks()}
          search={search}
          navigate={navigate}
        />
      </ListPage>

      <TasksDialogs />
    </>
  )
}

export function Tasks() {
  return (
    <TasksProvider>
      <TasksContent />
    </TasksProvider>
  )
}

export default Tasks
