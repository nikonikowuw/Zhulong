import { useAlgorithms } from '@/features/algorithms/contract'
import { useAllCameras } from '@/features/cameras/contract'
import { useTasks } from '../hooks/use-tasks'
import { ChannelTaskDialog } from './channel-task-dialog'
import { TaskDeleteDialog } from './task-delete-dialog'
import { useTasksContext } from './tasks-provider'

/**
 * 弹窗集中挂载：与 Cameras / Persons 一致，状态由 Provider 持有，
 * 领域数据各自通过 React Query 取（同一 queryKey 会被去重，不会重复请求）。
 */
export function TasksDialogs() {
  const {
    open,
    setOpen,
    currentRow,
    setCurrentRow,
    targetCameraId,
    setTargetCameraId,
    targetAlgorithmId,
    setTargetAlgorithmId,
  } = useTasksContext()

  const { data: camerasData } = useAllCameras()
  const { data: algorithms = [] } = useAlgorithms()
  const { data: tasks = [] } = useTasks()

  const cameras = camerasData?.items ?? []
  const initialCameraId = currentRow?.cameraId ?? targetCameraId ?? undefined

  const handleOpenChannelChange = (isOpen: boolean) => {
    if (!isOpen) {
      setOpen(null)
      setCurrentRow(null)
      setTargetCameraId(null)
      setTargetAlgorithmId(null)
    } else {
      setOpen('channelTask')
    }
  }

  return (
    <>
      <ChannelTaskDialog
        open={open === 'channelTask'}
        onOpenChange={handleOpenChannelChange}
        initialCameraId={initialCameraId}
        initialAlgorithmId={targetAlgorithmId ?? undefined}
        cameras={cameras}
        algorithms={algorithms}
        allTasks={tasks}
      />

      <TaskDeleteDialog
        task={currentRow}
        open={open === 'delete'}
        onOpenChange={(isOpen) => setOpen(isOpen ? 'delete' : null)}
      />
    </>
  )
}
