import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from '@tanstack/react-query'
import { gb28181Api } from '../api/gb28181-api'
import {
  type Gb28181Channel,
  type Gb28181Config,
  type Gb28181ConfigResponse,
  type Gb28181Device,
  type Gb28181RuntimeSnapshotStatus,
  type SyncCatalogResponse,
  type UpsertGb28181DeviceRequest,
} from '../data/schema'

export const GB28181_QUERY_KEYS = {
  all: ['gb28181'] as const,
  config: () => [...GB28181_QUERY_KEYS.all, 'config'] as const,
  runtime: () => [...GB28181_QUERY_KEYS.all, 'runtime'] as const,
  devices: () => [...GB28181_QUERY_KEYS.all, 'devices'] as const,
  channels: (deviceId: string) =>
    [...GB28181_QUERY_KEYS.all, 'channels', deviceId] as const,
}

/** 已保存配置 + 本进程启动配置与运行状态。 */
export function useGb28181Config(): UseQueryResult<
  Gb28181ConfigResponse,
  Error
> {
  return useQuery({
    queryKey: GB28181_QUERY_KEYS.config(),
    queryFn: () => gb28181Api.getConfig(),
    refetchInterval: 5000,
    refetchIntervalInBackground: false,
    staleTime: 10_000,
  })
}

/**
 * 运行态快照。
 *
 * 这是现场排障入口，因此按固定间隔轮询；窗口隐藏时停止，
 * 避免无人查看时持续打后端。
 */
export function useGb28181Runtime(): UseQueryResult<
  Gb28181RuntimeSnapshotStatus,
  Error
> {
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

/** 查询设备下的通道列表。 */
export function useGb28181Channels(
  deviceId: string | null
): UseQueryResult<Gb28181Channel[], Error> {
  return useQuery({
    queryKey: GB28181_QUERY_KEYS.channels(deviceId ?? ''),
    queryFn: () => gb28181Api.listChannels(deviceId!),
    enabled: !!deviceId,
    staleTime: 5_000,
  })
}

/** 手动触发目录同步指令。 */
export function useSyncGb28181Catalog(): UseMutationResult<
  SyncCatalogResponse,
  Error,
  string
> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (deviceId: string) => gb28181Api.syncCatalog(deviceId),
    onSuccess: (_data, deviceId) => {
      void queryClient.invalidateQueries({
        queryKey: GB28181_QUERY_KEYS.channels(deviceId),
      })
      void queryClient.invalidateQueries({
        queryKey: GB28181_QUERY_KEYS.devices(),
      })
    },
  })
}

/** 将指定通道显式添加为系统摄像机。 */
export function useAddGb28181ChannelCamera(): UseMutationResult<
  unknown,
  Error,
  { deviceId: string; channelId: string }
> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ deviceId, channelId }) =>
      gb28181Api.addChannelAsCamera(deviceId, channelId),
    onSuccess: (_data, { deviceId }) => {
      void queryClient.invalidateQueries({
        queryKey: GB28181_QUERY_KEYS.channels(deviceId),
      })
      void queryClient.invalidateQueries({
        queryKey: ['cameras'],
      })
    },
  })
}

/** 删除设备。 */
export function useDeleteGb28181Device(): UseMutationResult<
  void,
  Error,
  string
> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (deviceId: string) => gb28181Api.deleteDevice(deviceId),
    meta: { suppressGlobalToast: true },
    onSuccess: (_data, deviceId) => {
      void queryClient.invalidateQueries({
        queryKey: GB28181_QUERY_KEYS.devices(),
      })
      void queryClient.invalidateQueries({
        queryKey: GB28181_QUERY_KEYS.runtime(),
      })
      queryClient.removeQueries({
        queryKey: GB28181_QUERY_KEYS.channels(deviceId),
        exact: true,
      })
    },
  })
}
