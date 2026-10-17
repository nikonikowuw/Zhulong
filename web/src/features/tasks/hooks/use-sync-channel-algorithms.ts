import { useMutation, useQueryClient } from '@tanstack/react-query'
import { tasksApi } from '../api/tasks-api'
import { type AlgorithmConfigDraft, type Task } from '../data/schema'
import { TASKS_QUERY_KEY } from './use-tasks'

export interface SyncChannelAlgorithmsParams {
  cameraId: string
  drafts: AlgorithmConfigDraft[]
  existingTasks: Task[]
}

/**
 * 通道多算法批量差量同步 Hook
 */
export function useSyncChannelAlgorithms() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({
      cameraId,
      drafts,
      existingTasks,
    }: SyncChannelAlgorithmsParams) => {
      const existingMap = new Map(existingTasks.map((t) => [t.algorithmId, t]))
      const draftMap = new Map(drafts.map((d) => [d.algorithmId, d]))

      const promises: Promise<unknown>[] = []

      // 1. 查找新增与更新项
      for (const [algoId, draft] of draftMap.entries()) {
        const existing = existingMap.get(algoId)
        if (!existing) {
          promises.push(
            tasksApi.createTask({
              cameraId,
              algorithmId: algoId,
              sampleFps: draft.sampleFps,
              enabled: draft.enabled,
              configJson: draft.configJson,
            })
          )
        } else {
          const fpsChanged = existing.sampleFps !== draft.sampleFps
          const enabledChanged = existing.enabled !== draft.enabled
          const configChanged = existing.configJson !== draft.configJson

          if (fpsChanged || enabledChanged || configChanged) {
            promises.push(
              tasksApi.updateTask(existing.id, {
                sampleFps: draft.sampleFps,
                enabled: draft.enabled,
                configJson: draft.configJson,
              })
            )
          }
        }
      }

      // 2. 查找已取消勾选需删除的项
      for (const [algoId, existing] of existingMap.entries()) {
        if (!draftMap.has(algoId)) {
          promises.push(tasksApi.deleteTask(existing.id))
        }
      }

      await Promise.all(promises)
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: TASKS_QUERY_KEY })
    },
  })
}
