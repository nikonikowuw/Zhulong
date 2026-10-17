import { useMemo } from 'react'
import { type Algorithm } from '@/features/algorithms/data/schema'
import { type Camera } from '@/features/cameras/data/schema'
import {
  type ChannelTaskGroup,
  type ChannelTaskItem,
  type Task,
} from '../data/schema'

/**
 * 通道聚合 Hook：将摄像头、算法包与任务实体组合派生为以摄像头为视角的通道任务组
 */
export function useChannelTasks(
  cameras: Camera[],
  algorithms: Algorithm[],
  tasks: Task[]
): ChannelTaskGroup[] {
  return useMemo(() => {
    const algoMap = new Map(algorithms.map((a) => [a.id, a]))
    const tasksByCamera = new Map<string, Task[]>()

    for (const t of tasks) {
      const list = tasksByCamera.get(t.cameraId) ?? []
      list.push(t)
      tasksByCamera.set(t.cameraId, list)
    }

    return cameras.map((camera) => {
      const cameraTasks = tasksByCamera.get(camera.id) ?? []
      let totalChannelPoints = 0
      let activeCount = 0

      const items: ChannelTaskItem[] = cameraTasks.map((task) => {
        const algorithm = algoMap.get(task.algorithmId)
        const cost = algorithm?.computeCost ?? 10
        const points = cost * task.sampleFps
        if (task.enabled) {
          totalChannelPoints += points
          activeCount++
        }
        return {
          task,
          algorithm,
          computePoints: points,
        }
      })

      return {
        camera,
        items,
        totalChannelPoints,
        activeCount,
        totalCount: items.length,
      }
    })
  }, [cameras, algorithms, tasks])
}
