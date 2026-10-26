import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from '@tanstack/react-query'
import { gb28181Api } from '../api/gb28181-api'
import {
  type Gb28181Config,
  type Gb28181ConfigResponse,
  type Gb28181Device,
  type Gb28181Snapshot,
  type UpsertGb28181DeviceRequest,
} from '../data/schema'

export const GB28181_QUERY_KEYS = {
  all: ['gb28181'] as const,
  config: () => [...GB28181_QUERY_KEYS.all, 'config'] as const,
  runtime: () => [...GB28181_QUERY_KEYS.all, 'runtime'] as const,
  devices: () => [...GB28181_QUERY_KEYS.all, 'devices'] as const,
}

/** 已保存配置 + 本进程启动配置与运行状态。 */
export function useGb28181Config(): UseQueryResult<
  Gb28181ConfigResponse,
  Error
> {
  return useQuery({
    queryKey: GB28181_QUERY_KEYS.config(),
    queryFn: () => gb28181Api.getConfig(),
    staleTime: 10_000,
  })
}

/**
 * 运行态快照。
 *
 * 这是现场排障入口，因此按固定间隔轮询；窗口隐藏时停止，
 * 避免无人查看时持续打后端。
 */
export function useGb28181Runtime(): UseQueryResult<Gb28181Snapshot, Error> {
  return useQuery({
    queryKey: GB28181_QUERY_KEYS.runtime(),
    queryFn: () => gb28181Api.getRuntime(),
    refetchInterval: 5000,
    refetchIntervalInBackground: false,
    staleTime: 0,
  })
}

/** 设备 allowlist（后端已按设备编号排序，无需分页参数）。 */
export function useGb28181Devices(): UseQueryResult<Gb28181Device[], Error> {
  return useQuery({
    queryKey: GB28181_QUERY_KEYS.devices(),
    queryFn: () => gb28181Api.listDevices(),
    staleTime: 10_000,
  })
}

/**
 * 保存目标配置。
 *
 * 全局 toast 由表单抑制（`suppressGlobalToast`），因为它要把后端字段级
 * details 映射到具体输入框，重复弹窗会遮住表单。
 */
export function useUpdateGb28181Config(): UseMutationResult<
  Gb28181ConfigResponse,
  Error,
  Gb28181Config
> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (config: Gb28181Config) => gb28181Api.updateConfig(config),
    meta: { suppressGlobalToast: true },
    onSuccess: (data) => {
      // 运行态快照里的 restartRequired 也需要更新，因此整体失效。
      queryClient.setQueryData(GB28181_QUERY_KEYS.config(), data)
      void queryClient.invalidateQueries({
        queryKey: GB28181_QUERY_KEYS.runtime(),
      })
    },
  })
}

export function useUpsertGb28181Device(): UseMutationResult<
  Gb28181Device,
  Error,
  { deviceId: string; payload: UpsertGb28181DeviceRequest }
> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ deviceId, payload }) =>
      gb28181Api.upsertDevice(deviceId, payload),
    meta: { suppressGlobalToast: true },
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: GB28181_QUERY_KEYS.devices(),
      })
      void queryClient.invalidateQueries({
        queryKey: GB28181_QUERY_KEYS.runtime(),
      })
    },
  })
}
