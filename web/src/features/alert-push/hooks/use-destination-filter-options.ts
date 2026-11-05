import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { algorithmsApi } from '@/features/algorithms/api/algorithms-api'
import { camerasApi } from '@/features/cameras/api/cameras-api'

export interface FilterOption {
  id: string
  name: string
}

export function useDestinationFilterOptions() {
  const algosQuery = useQuery({
    queryKey: ['algorithms'],
    queryFn: () => algorithmsApi.getAlgorithms(),
    staleTime: 5 * 60 * 1000,
  })

  const camerasQuery = useQuery({
    queryKey: ['cameras', 'filter-options'],
    queryFn: () => camerasApi.getCameras({ pageSize: 100 }),
    staleTime: 5 * 60 * 1000,
  })

  const algorithms = useMemo<FilterOption[]>(
    () => algosQuery.data?.map((a) => ({ id: a.id, name: a.name })) ?? [],
    [algosQuery.data]
  )

  const cameras = useMemo<FilterOption[]>(
    () =>
      camerasQuery.data?.items.map((c) => ({ id: c.id, name: c.name })) ?? [],
    [camerasQuery.data]
  )

  return {
    algorithms,
    cameras,
    isLoading: algosQuery.isLoading || camerasQuery.isLoading,
  }
}
