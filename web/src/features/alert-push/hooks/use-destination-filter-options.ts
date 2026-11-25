import { useMemo } from 'react'
import { useAlgorithms } from '@/features/algorithms/contract'
import { useAllCameras } from '@/features/cameras/contract'

export interface FilterOption {
  id: string
  name: string
}

export function useDestinationFilterOptions() {
  const algosQuery = useAlgorithms()
  const camerasQuery = useAllCameras()

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
    isAlgosLoading: algosQuery.isLoading,
    isCamerasLoading: camerasQuery.isLoading,
    isError: algosQuery.isError || camerasQuery.isError,
    isAlgosError: algosQuery.isError,
    isCamerasError: camerasQuery.isError,
    refetchAlgorithms: algosQuery.refetch,
    refetchCameras: camerasQuery.refetch,
  }
}
