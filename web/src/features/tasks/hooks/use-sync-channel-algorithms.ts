import { useMutation, useQueryClient } from '@tanstack/react-query'
import { tasksApi } from '../api/tasks-api'
import { type Rect } from '../data/region-config'
import { type AlgorithmConfigDraft, type Task } from '../data/schema'
import { TASKS_QUERY_KEY } from './use-tasks'

export interface SyncChannelAlgorithmsParams {
  cameraId: string
  enabled?: boolean
  /** 任务级裁剪窗口；`null` = 全幅。整任务 upsert，省略会保留旧值。 */
  crop?: Rect | null
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
      crop = null,
      drafts,
    }: SyncChannelAlgorithmsParams) => {
      await tasksApi.saveChannelTask({
        cameraId,
        enabled,
        crop,
        algorithms: drafts.map((d) => ({
          algorithmId: d.algorithmId,
          sampleFps: d.sampleFps,
          enabled: d.enabled,
          configJson: d.configJson,
          regionConfigJson: d.regionConfigJson,
          scheduleJson: d.scheduleJson,
        })),
      })
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: TASKS_QUERY_KEY })
    },
  })
}
