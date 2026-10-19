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
    const taskByCamera = new Map<string, Task>()

    for (const t of tasks) {
      taskByCamera.set(t.cameraId, t)
    }

    return cameras.map((camera) => {
      const task = taskByCamera.get(camera.id)
      let totalChannelPoints = 0
      let activeCount = 0

      const items: ChannelTaskItem[] = []
      if (task) {
        for (const sub of task.algorithms) {
          const algorithm = algoMap.get(sub.algorithmId)
          const cost = algorithm?.computeCost ?? 10
          const points = cost * sub.sampleFps
          if (task.enabled && sub.enabled) {
            totalChannelPoints += points
            activeCount++
          }
          items.push({
            taskId: task.id,
            task,
            algorithmItem: sub,
            algorithm,
            computePoints: points,
          })
        }
      }

      return {
        camera,
        task,
        items,
        totalChannelPoints,
        activeCount,
        totalCount: items.length,
      }
    })
  }, [cameras, algorithms, tasks])
}
