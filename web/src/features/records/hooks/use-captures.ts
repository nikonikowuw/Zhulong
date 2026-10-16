import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import { recordsApi } from '../api/records-api'
import {
  type Capture,
  type CaptureListResponse,
  type GetCapturesParams,
} from '../data/schema'

export const CAPTURES_QUERY_KEY = ['records', 'captures'] as const

export function useCaptures(
  params?: GetCapturesParams
): UseQueryResult<CaptureListResponse, Error> {
  return useQuery({
    queryKey: [...CAPTURES_QUERY_KEY, params],
    queryFn: () => recordsApi.getCaptures(params),
    placeholderData: (previousData) => previousData,
  })
}

export function useCapture(id?: string): UseQueryResult<Capture, Error> {
  return useQuery({
    queryKey: [...CAPTURES_QUERY_KEY, 'detail', id],
    queryFn: () =>
      id ? recordsApi.getCapture(id) : Promise.reject(new Error('No ID')),
    enabled: Boolean(id),
  })
}
