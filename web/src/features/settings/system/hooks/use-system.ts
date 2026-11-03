import { useQuery } from '@tanstack/react-query'
import { systemApi } from '../api/system-api'

export const SYSTEM_HARDWARE_QUERY_KEY = ['system', 'hardware']

export function useSystemHardware() {
  return useQuery({
    queryKey: SYSTEM_HARDWARE_QUERY_KEY,
    queryFn: () => systemApi.getHardware(),
    refetchInterval: 2500,
    staleTime: 1000,
  })
}
