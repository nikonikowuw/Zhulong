import { useMemo } from 'react'
import { type Algorithm } from '@/features/algorithms/data/schema'
import { type Task } from '../data/schema'

export const MAX_COMPUTE_POINTS = 1000

export interface TaskComputeBudget {
  totalUsedPoints: number
  maxPoints: number
  remainingPoints: number
  percent: number
  isOverBudget: boolean
  isWarning: boolean
  enabledCount: number
  totalCount: number
}

/**
 * 纯计算派生全局算力预算消费情况
 */
export function useTaskComputeBudget(
  tasks: Task[],
  algorithms: Algorithm[]
): TaskComputeBudget {
  return useMemo(() => {
    const algoMap = new Map(algorithms.map((a) => [a.id, a]))

    let totalUsedPoints = 0
    let enabledCount = 0

    for (const t of tasks) {
      if (t.enabled) {
        enabledCount++
        const algo = algoMap.get(t.algorithmId)
        const cost = algo?.computeCost ?? 10
        totalUsedPoints += cost * t.sampleFps
      }
    }

    const percent = Math.round((totalUsedPoints / MAX_COMPUTE_POINTS) * 100)
    const isOverBudget = totalUsedPoints > MAX_COMPUTE_POINTS
    const isWarning = totalUsedPoints > 800 && !isOverBudget
    const remainingPoints = Math.max(0, MAX_COMPUTE_POINTS - totalUsedPoints)

    return {
      totalUsedPoints,
      maxPoints: MAX_COMPUTE_POINTS,
      remainingPoints,
      percent,
      isOverBudget,
      isWarning,
      enabledCount,
      totalCount: tasks.length,
    }
  }, [tasks, algorithms])
}
