import { useQuery } from '@tanstack/react-query'
import { recordsApi } from '../api/records-api'

export const ALARM_TYPES_QUERY_KEY = ['records', 'alarm-types'] as const

export function useAlarmTypes() {
  return useQuery({
    queryKey: ALARM_TYPES_QUERY_KEY,
    queryFn: () => recordsApi.getAlarmTypes(),
    staleTime: 60_000,
  })
}
