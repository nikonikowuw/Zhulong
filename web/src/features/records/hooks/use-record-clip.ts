import { useQuery, useQueryClient } from '@tanstack/react-query'
import { recordsApi } from '../api/records-api'
import { type ClipInfo } from '../data/schema'
import { ALARMS_QUERY_KEY } from './use-alarms'
import { RECOGNITIONS_QUERY_KEY } from './use-recognitions'

export type ClipRecordKind = 'alarm' | 'recognition'

export interface RecordClipRefresh {
  /** 刷新后拿到的片段；`undefined` = 尚未刷新（调用方回退到列表快照）。 */
  clip: ClipInfo | null | undefined
  refresh: () => void
  isRefreshing: boolean
  isError: boolean
}

/**
 * 详情里录像片段的「手动刷新」数据源。
 *
 * 详情弹窗拿到的是列表行快照；片段处于 `recording` 时需要重新拉取单条详情
 * 才能看到最终状态。`enabled: false` —— 只在用户点刷新时请求，不在打开弹窗时
 * 额外多打一次接口；`gcTime: 0` —— 面板卸载即丢弃，下次打开不会拿一份比列表
 * 快照还旧的缓存覆盖它。
 */
export function useRecordClipRefresh(
  kind: ClipRecordKind,
  recordId: string
): RecordClipRefresh {
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey:
      kind === 'alarm'
        ? [...ALARMS_QUERY_KEY, 'clip', recordId]
        : [...RECOGNITIONS_QUERY_KEY, 'clip', recordId],
    queryFn: async (): Promise<ClipInfo | null> => {
      const record =
        kind === 'alarm'
          ? await recordsApi.getAlarm(recordId)
          : await recordsApi.getRecognition(recordId)
      return record.clip ?? null
    },
    enabled: false,
    gcTime: 0,
  })

  return {
    clip: query.data,
    refresh: () => {
      void query.refetch().then((res) => {
        if (res.status === 'success' && res.data?.status !== 'recording') {
          void queryClient.invalidateQueries({
            queryKey:
              kind === 'alarm' ? ALARMS_QUERY_KEY : RECOGNITIONS_QUERY_KEY,
          })
        }
      })
    },
    isRefreshing: query.isFetching,
    isError: query.isError,
  }
}
