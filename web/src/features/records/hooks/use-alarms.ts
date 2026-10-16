import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import { recordsApi } from '../api/records-api'
import {
  type Alarm,
  type AlarmListResponse,
  type GetAlarmsParams,
} from '../data/schema'

export const ALARMS_QUERY_KEY = ['records', 'alarms'] as const

export function useAlarms(
  params?: GetAlarmsParams
): UseQueryResult<AlarmListResponse, Error> {
  return useQuery({
    queryKey: [...ALARMS_QUERY_KEY, params],
    queryFn: () => recordsApi.getAlarms(params),
    placeholderData: (previousData) => previousData,
  })
}

export function useAlarm(id?: string): UseQueryResult<Alarm, Error> {
  return useQuery({
    queryKey: [...ALARMS_QUERY_KEY, 'detail', id],
    queryFn: () =>
      id ? recordsApi.getAlarm(id) : Promise.reject(new Error('No ID')),
    enabled: Boolean(id),
  })
}
