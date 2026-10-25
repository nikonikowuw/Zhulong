import { type Algorithm } from '@/features/algorithms/data/schema'
import { type Camera } from '@/features/cameras/data/schema'
import { type Task } from './schema'

/**
 * 明细表格行视图模型：把「通道任务 × 算法订阅」拍平成一行。
 *
 * 主键语义有两层，因此两层都保留：
 * - `taskId` 是 `analysis_tasks.id`（`/tasks/{id}` 与审计日志用的就是它）；
 * - `id` 是 `task_algorithms.id`（update payload 里的 `algorithms[].id`）。
 */
export interface TaskRow {
  /** task_algorithms.id */
  id: string
  /** analysis_tasks.id */
  taskId: string
  task: Task
  cameraId: string
  cameraName: string
  algorithmId: string
  algorithmName: string
  /** 算法单帧算力开销（点/帧） */
  computeCost: number
  sampleFps: number
  /** 通道开关 ∧ 算法开关 */
  enabled: boolean
  isChannelEnabled: boolean
  isAlgoEnabled: boolean
  /** computeCost × sampleFps */
  computePoints: number
  algorithm?: Algorithm
  createdAt: string
  updatedAt: string
  /** 列筛选用的派生状态字符串 */
  status: 'running' | 'paused'
}

/**
 * 派生明细分表行。摄像头 / 算法名称在此解析，避免每个单元格重复查表。
 */
export function toTaskRows(
  tasks: Task[],
  cameras: Camera[],
  algorithms: Algorithm[]
): TaskRow[] {
  const cameraMap = new Map(cameras.map((c) => [c.id, c]))
  const algorithmMap = new Map(algorithms.map((a) => [a.id, a]))

  const rows: TaskRow[] = []

  for (const task of tasks) {
    for (const sub of task.algorithms ?? []) {
      const algorithm = algorithmMap.get(sub.algorithmId)
      const computeCost = algorithm?.computeCost ?? 10
      const enabled = task.enabled && sub.enabled

      rows.push({
        id: sub.id,
        taskId: task.id,
        task,
        cameraId: task.cameraId,
        cameraName: cameraMap.get(task.cameraId)?.name ?? task.cameraId,
        algorithmId: sub.algorithmId,
        algorithmName: algorithm?.name ?? sub.algorithmId,
        computeCost,
        sampleFps: sub.sampleFps,
        enabled,
        isChannelEnabled: task.enabled,
        isAlgoEnabled: sub.enabled,
        computePoints: computeCost * sub.sampleFps,
        algorithm,
        createdAt: sub.createdAt,
        updatedAt: sub.updatedAt,
        status: enabled ? 'running' : 'paused',
      })
    }
  }

  return rows
}
