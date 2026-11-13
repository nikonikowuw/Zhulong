import { useQuery } from '@tanstack/react-query'
import { recordsApi } from '../api/records-api'

export const CAPTURE_TARGET_TYPES_QUERY_KEY = [
  'records',
  'capture-target-types',
] as const

export function useCaptureTargetTypes(options?: { enabled?: boolean }) {
  return useQuery({
    queryKey: CAPTURE_TARGET_TYPES_QUERY_KEY,
    queryFn: () => recordsApi.getCaptureTargetTypes(),
    enabled: options?.enabled ?? true,
    staleTime: 60_000,
  })
}
