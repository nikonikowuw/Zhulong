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
