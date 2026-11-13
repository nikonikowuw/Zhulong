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
    enabled: params !== undefined,
    placeholderData: (previousData, previousQuery) => {
      const previousParams = previousQuery?.queryKey[2]
      const previousTargetType =
        typeof previousParams === 'object' &&
        previousParams !== null &&
        'targetType' in previousParams &&
        typeof previousParams.targetType === 'string'
          ? previousParams.targetType
          : undefined

      return previousTargetType === params?.targetType
        ? previousData
        : undefined
    },
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
