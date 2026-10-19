import { useMutation, useQueryClient } from '@tanstack/react-query'
import { tasksApi } from '../api/tasks-api'
import { type AlgorithmConfigDraft, type Task } from '../data/schema'
import { TASKS_QUERY_KEY } from './use-tasks'

export interface SyncChannelAlgorithmsParams {
  cameraId: string
  enabled?: boolean
  drafts: AlgorithmConfigDraft[]
  existingTasks?: Task[]
}

/**
 * 通道多算法原子同步 Hook
 */
export function useSyncChannelAlgorithms() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({
      cameraId,
      enabled = true,
      drafts,
    }: SyncChannelAlgorithmsParams) => {
      await tasksApi.saveChannelTask({
        cameraId,
        enabled,
        algorithms: drafts.map((d) => ({
          algorithmId: d.algorithmId,
          sampleFps: d.sampleFps,
          enabled: d.enabled,
          configJson: d.configJson,
          regionConfigJson: d.regionConfigJson,
        })),
      })
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: TASKS_QUERY_KEY })
    },
  })
}
