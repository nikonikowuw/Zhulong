import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { storageApi } from '../api/storage-api'
import { type PathTestPayload, type UpdateConfigPayload } from '../types'

export const STORAGE_STATUS_QUERY_KEY = ['system', 'storage', 'status']
export const STORAGE_CONFIG_QUERY_KEY = ['system', 'storage', 'config']

export function useStorageStatus() {
  return useQuery({
    queryKey: STORAGE_STATUS_QUERY_KEY,
    queryFn: () => storageApi.getStatus(),
    refetchInterval: 15_000,
  })
}

export function useStorageConfig() {
  return useQuery({
    queryKey: STORAGE_CONFIG_QUERY_KEY,
    queryFn: () => storageApi.getConfig(),
  })
}

export function useUpdateStorageConfig() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (payload: UpdateConfigPayload) => storageApi.updateConfig(payload),
    onSuccess: (data) => {
      queryClient.setQueryData(STORAGE_STATUS_QUERY_KEY, data)
      void queryClient.invalidateQueries({ queryKey: STORAGE_CONFIG_QUERY_KEY })
    },
  })
}

export function useTestStoragePath() {
  return useMutation({
    mutationFn: (payload: PathTestPayload) => storageApi.testPath(payload),
  })
}
