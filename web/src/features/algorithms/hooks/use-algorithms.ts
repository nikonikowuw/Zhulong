import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { algorithmsApi, type UploadProgressCallback } from '../api/algorithms-api'

export const ALGORITHMS_QUERY_KEY = ['algorithms']

/**
 * 查询所有已安装算法包 Hook
 */
export function useAlgorithms() {
  return useQuery({
    queryKey: ALGORITHMS_QUERY_KEY,
    queryFn: () => algorithmsApi.getAlgorithms(),
    staleTime: 5000,
  })
}

/**
 * 安装/上传算法包 Mutation Hook
 */
export function useInstallAlgorithm(options?: {
  suppressGlobalToast?: boolean
}) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({
      file,
      onProgress,
    }: {
      file: File
      onProgress?: UploadProgressCallback
    }) => algorithmsApi.installAlgorithm(file, onProgress),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ALGORITHMS_QUERY_KEY })
    },
    meta: {
      suppressGlobalToast: options?.suppressGlobalToast,
    },
  })
}

/**
 * 查询卸载影响面。
 *
 * 只在弹窗打开时启用（`enabled` 由调用方传入），避免为每张表格行都发一次请求。
 * 刻意不设 staleTime：用户可能刚建完任务就回来卸载，缓存会让提示数字过期。
 */
export function useDeletionImpact(algorithmId: string | null, enabled: boolean) {
  return useQuery({
    queryKey: ['algorithms', algorithmId, 'deletion-impact'],
    queryFn: () => algorithmsApi.getDeletionImpact(algorithmId as string),
    enabled: enabled && Boolean(algorithmId),
    staleTime: 0,
    gcTime: 0,
    retry: false,
  })
}

/**
 * 卸载/删除算法包 Mutation Hook
 */
export function useDeleteAlgorithm() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) => algorithmsApi.deleteAlgorithm(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ALGORITHMS_QUERY_KEY })
    },
  })
}

/**
 * 启用算法 Mutation Hook
 */
export function useEnableAlgorithm() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) => algorithmsApi.enableAlgorithm(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ALGORITHMS_QUERY_KEY })
    },
  })
}

/**
 * 停用算法 Mutation Hook
 */
export function useDisableAlgorithm() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) => algorithmsApi.disableAlgorithm(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ALGORITHMS_QUERY_KEY })
    },
  })
}

/**
 * 重置熔断 Mutation Hook
 *
 * `suppressGlobalToast` 是刻意声明的：调用方（AlgorithmResetDialog）把 409/500
 * 渲染成弹窗内的留存 Banner 并提供重试，若再叠加全局兜底 Toast 就是双重反馈。
 */
export function useResetAlgorithm() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) => algorithmsApi.resetAlgorithm(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ALGORITHMS_QUERY_KEY })
    },
    meta: {
      suppressGlobalToast: true,
    },
  })
}
