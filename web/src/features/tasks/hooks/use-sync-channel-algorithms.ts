import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  ALGORITHMS_QUERY_KEY,
  algorithmsApi,
} from '@/features/algorithms/contract'
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
  /** 保存成功后一并激活的已禁用算法 ID，保持全流程共享同一个 pending 状态。 */
  autoEnableAlgorithmIds?: string[]
}

export interface SyncChannelAlgorithmsResult {
  enabledCount: number
  enableFailed: boolean
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
      autoEnableAlgorithmIds,
    }: SyncChannelAlgorithmsParams): Promise<SyncChannelAlgorithmsResult> => {
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

      if (!autoEnableAlgorithmIds || autoEnableAlgorithmIds.length === 0) {
        return { enabledCount: 0, enableFailed: false }
      }

      try {
        await Promise.all(
          autoEnableAlgorithmIds.map((algoId) =>
            algorithmsApi.enableAlgorithm(algoId)
          )
        )
        return {
          enabledCount: autoEnableAlgorithmIds.length,
          enableFailed: false,
        }
      } catch {
        return { enabledCount: 0, enableFailed: true }
      }
    },
    onSettled: (_data, _error, variables) => {
      void queryClient.invalidateQueries({ queryKey: TASKS_QUERY_KEY })
      if (variables.autoEnableAlgorithmIds?.length) {
        void queryClient.invalidateQueries({ queryKey: ALGORITHMS_QUERY_KEY })
      }
    },
  })
}
