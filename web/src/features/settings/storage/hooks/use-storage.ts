import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { storageApi } from '../api/storage-api'
import {
  type ConnectionTestPayload,
  type PathTestPayload,
  type StartMigrationPayload,
  type UpdateConfigPayload,
} from '../types'

export const STORAGE_STATUS_QUERY_KEY = ['system', 'storage', 'status']
export const STORAGE_CONFIG_QUERY_KEY = ['system', 'storage', 'config']
export const STORAGE_MIGRATION_LATEST_QUERY_KEY = [
  'system',
  'storage',
  'migration',
  'latest',
]

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
    mutationFn: (payload: UpdateConfigPayload) =>
      storageApi.updateConfig(payload),
    onSuccess: (data) => {
      queryClient.setQueryData(STORAGE_STATUS_QUERY_KEY, data)
      void queryClient.invalidateQueries({ queryKey: STORAGE_CONFIG_QUERY_KEY })
    },
  })
}

export function useTestStoragePath(options?: {
  suppressGlobalToast?: boolean
}) {
  return useMutation({
    mutationFn: (payload: PathTestPayload) => storageApi.testPath(payload),
    meta: {
      suppressGlobalToast: options?.suppressGlobalToast,
    },
  })
}

export function useTestStorageConnection(options?: {
  suppressGlobalToast?: boolean
}) {
  return useMutation({
    mutationFn: (payload: ConnectionTestPayload) =>
      storageApi.testConnection(payload),
    meta: {
      suppressGlobalToast: options?.suppressGlobalToast,
    },
  })
}

export function useLatestStorageMigration(enabled = true) {
  return useQuery({
    queryKey: STORAGE_MIGRATION_LATEST_QUERY_KEY,
    queryFn: () => storageApi.getLatestMigration(),
    enabled,
    refetchInterval: (query) => {
      const status = query.state.data?.status
      if (
        status === 'PENDING' ||
        status === 'EXTRACTING' ||
        status === 'PROCESSING'
      ) {
        return 2_000
      }
      return false
    },
  })
}

export function useStartStorageMigration() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (payload: StartMigrationPayload) =>
      storageApi.startMigration(payload),
    onSuccess: (job) => {
      queryClient.setQueryData(STORAGE_MIGRATION_LATEST_QUERY_KEY, job)
    },
  })
}
