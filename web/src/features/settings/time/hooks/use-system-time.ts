import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { timeApi } from '../api/time-api'
import { type ManualTimePayload, type UpdateConfigPayload } from '../types'

export const SYSTEM_TIME_QUERY_KEY = ['system', 'time']

export function useSystemTime() {
  return useQuery({
    queryKey: SYSTEM_TIME_QUERY_KEY,
    queryFn: () => timeApi.getStatus(),
    refetchInterval: 30_000,
  })
}

export function useUpdateConfig() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (payload: UpdateConfigPayload) => timeApi.updateConfig(payload),
    onSuccess: (data) => {
      queryClient.setQueryData(SYSTEM_TIME_QUERY_KEY, data)
    },
  })
}

export function useSyncNow() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => timeApi.syncNow(),
    onSuccess: (data) => {
      queryClient.setQueryData(SYSTEM_TIME_QUERY_KEY, data)
    },
  })
}

export function useManualTime() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (payload: ManualTimePayload) => timeApi.setManualTime(payload),
    onSuccess: (data) => {
      queryClient.setQueryData(SYSTEM_TIME_QUERY_KEY, data)
    },
  })
}
