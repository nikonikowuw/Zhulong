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

      // 阶段 1：先删除已取消勾选的任务，确保在后端立刻释放 NPU 算力配额，避免算力假阳性超限
      const deletePromises: Promise<void>[] = []
      for (const [algoId, existing] of existingMap.entries()) {
        if (!draftMap.has(algoId)) {
          deletePromises.push(tasksApi.deleteTask(existing.id))
        }
      }
      if (deletePromises.length > 0) {
        await Promise.all(deletePromises)
      }

      // 阶段 2：算力配额腾出后，再并行执行新增与参数更新
      const upsertPromises: Promise<unknown>[] = []
      for (const [algoId, draft] of draftMap.entries()) {
        const existing = existingMap.get(algoId)
        if (!existing) {
          upsertPromises.push(
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
            upsertPromises.push(
              tasksApi.updateTask(existing.id, {
                sampleFps: draft.sampleFps,
                enabled: draft.enabled,
                configJson: draft.configJson,
              })
            )
          }
        }
      }
      if (upsertPromises.length > 0) {
        await Promise.all(upsertPromises)
      }
    },
    // 无论是成功还是发生错误中断，都必须失效缓存，杜绝前端 UI 与后端状态漂移
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: TASKS_QUERY_KEY })
    },
  })
}
