import { useEffect, useMemo, useState } from 'react'
import { type Algorithm } from '@/features/algorithms/contract'
import {
  calculatePeakCompute,
  isScheduleActiveAt,
  type SchedulableItem,
} from '../data/schedule-config'
import { type Task } from '../data/schema'

export const MAX_COMPUTE_POINTS = 1000
export const DEFAULT_COMPUTE_COST = 10

export interface TaskComputeBudget {
  peakUsedPoints: number
  currentUsedPoints: number
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
 * 纯计算派生全局算力预算消费情况（支持峰值与当前瞬时双指标）
 * 包含低频心跳（默认 60 秒刷新内部刻度），避免页面长期驻留时当前瞬时算力无法随时间推移更新。
 */
export function useTaskComputeBudget(
  tasks: Task[],
  algorithms: Algorithm[],
  refreshIntervalMs = 60_000
): TaskComputeBudget {
  const [tick, setTick] = useState(0)

  useEffect(() => {
    if (refreshIntervalMs <= 0) return
    const timer = setInterval(() => {
      setTick((prev) => (prev + 1) % 1_000_000)
    }, refreshIntervalMs)
    return () => clearInterval(timer)
  }, [refreshIntervalMs])

  return useMemo(() => {
    // 引用 tick 使得定时器触发时能重新评估 isScheduleActiveAt
    void tick
    const algoMap = new Map(algorithms.map((a) => [a.id, a]))

    let enabledCount = 0
    let totalAlgoCount = 0
    let currentUsedPoints = 0
    const items: SchedulableItem[] = []

    for (const t of tasks) {
      for (const sub of t.algorithms ?? []) {
        totalAlgoCount++
        if (t.enabled && sub.enabled) {
          enabledCount++
          const algo = algoMap.get(sub.algorithmId)
          const cost = algo?.computeCost ?? DEFAULT_COMPUTE_COST
          const points = cost * sub.sampleFps
          items.push({
            points,
            schedule: sub.scheduleJson,
          })
          if (isScheduleActiveAt(sub.scheduleJson)) {
            currentUsedPoints += points
          }
        }
      }
    }

    const peakUsedPoints = calculatePeakCompute(items)
    const totalUsedPoints = peakUsedPoints

    const percent = Math.round((totalUsedPoints / MAX_COMPUTE_POINTS) * 100)
    const isOverBudget = totalUsedPoints > MAX_COMPUTE_POINTS
    const isWarning = totalUsedPoints > 800 && !isOverBudget
    const remainingPoints = Math.max(0, MAX_COMPUTE_POINTS - totalUsedPoints)

    return {
      peakUsedPoints,
      currentUsedPoints,
      totalUsedPoints,
      maxPoints: MAX_COMPUTE_POINTS,
      remainingPoints,
      percent,
      isOverBudget,
      isWarning,
      enabledCount,
      totalCount: totalAlgoCount,
    }
  }, [tasks, algorithms, tick])
}
