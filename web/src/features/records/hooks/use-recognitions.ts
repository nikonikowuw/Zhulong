import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import { recordsApi } from '../api/records-api'
import {
  type GetRecognitionsParams,
  type Recognition,
  type RecognitionListResponse,
} from '../data/schema'

export const RECOGNITIONS_QUERY_KEY = ['records', 'recognitions'] as const

export function useRecognitions(
  params?: GetRecognitionsParams
): UseQueryResult<RecognitionListResponse, Error> {
  return useQuery({
    queryKey: [...RECOGNITIONS_QUERY_KEY, params],
    queryFn: () => recordsApi.getRecognitions(params),
    placeholderData: (previousData) => previousData,
  })
}

export function useRecognition(
  id?: string
): UseQueryResult<Recognition, Error> {
  return useQuery({
    queryKey: [...RECOGNITIONS_QUERY_KEY, 'detail', id],
    queryFn: () =>
      id ? recordsApi.getRecognition(id) : Promise.reject(new Error('No ID')),
    enabled: Boolean(id),
  })
}
