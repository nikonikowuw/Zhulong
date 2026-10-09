import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  networkApi,
  type InterfaceConfigPayload,
  type InterfaceInfo,
  type PingResult,
  type TransactionState,
} from '../api/network-api'

export const NETWORK_QUERY_KEYS = {
  all: ['network'] as const,
  interfaces: () => [...NETWORK_QUERY_KEYS.all, 'interfaces'] as const,
  status: () => [...NETWORK_QUERY_KEYS.all, 'status'] as const,
}

/**
 * 查询物理网卡列表
 */
export function useNetworkInterfaces() {
  return useQuery<InterfaceInfo[]>({
    queryKey: NETWORK_QUERY_KEYS.interfaces(),
    queryFn: () => networkApi.getInterfaces(),
    staleTime: 5000,
  })
}

/**
 * 查询两阶段看门狗事务状态（若存在活跃事务则定期轮询）
 */
export function useNetworkStatus() {
  return useQuery<TransactionState>({
    queryKey: NETWORK_QUERY_KEYS.status(),
    queryFn: () => networkApi.getStatus(),
    refetchInterval: (query) => {
      const state = query.state.data
      if (state && state.status === 'pending_confirm') {
        return 2000 // 处于等待确认期时，每 2 秒同步一次服务端状态
      }
      return false
    },
  })
}

/**
 * 提交网卡配置并开启看门狗事务
 */
export function useApplyConfigMutation() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({
      name,
      payload,
    }: {
      name: string
      payload: InterfaceConfigPayload
    }) => networkApi.applyConfig(name, payload),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: NETWORK_QUERY_KEYS.all })
    },
  })
}

/**
 * 确认配置生效并解除看门狗
 */
export function useConfirmMutation() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (token?: string) => networkApi.confirm(token),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: NETWORK_QUERY_KEYS.all })
    },
  })
}

/**
 * 立即取消试运行并恢复旧配置
 */
export function useRollbackMutation() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (token?: string) => networkApi.rollback(token),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: NETWORK_QUERY_KEYS.all })
    },
  })
}

/**
 * 连通性测试 (Ping)
 */
export function usePingMutation() {
  return useMutation<PingResult, Error, string>({
    mutationFn: (target: string) => networkApi.ping(target),
  })
}
