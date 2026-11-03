import { useQuery } from '@tanstack/react-query'
import { systemApi } from '../api/system-api'

export const SYSTEM_HARDWARE_QUERY_KEY = ['system', 'hardware']

export interface UseSystemHardwareOptions {
  isPaused?: boolean
}

export function useSystemHardware(options: UseSystemHardwareOptions = {}) {
  const { isPaused = false } = options

  return useQuery({
    queryKey: SYSTEM_HARDWARE_QUERY_KEY,
    queryFn: () => systemApi.getHardware(),
    refetchInterval: isPaused ? false : 2500,
    staleTime: 1000,
  })
}
