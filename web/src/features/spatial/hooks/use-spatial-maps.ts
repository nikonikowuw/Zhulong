import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { spatialApi } from '../api/spatial-api'

export const SPATIAL_MAPS_KEY = ['spatial', 'maps']

export function useSpatialMaps() {
  return useQuery({
    queryKey: SPATIAL_MAPS_KEY,
    queryFn: () => spatialApi.getMaps(),
  })
}

export function useUploadMap() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ name, file }: { name: string; file: File }) =>
      spatialApi.uploadMap(name, file),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: SPATIAL_MAPS_KEY })
    },
  })
}

export function useUpdateMapName() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ id, name }: { id: string; name: string }) =>
      spatialApi.updateMapName(id, name),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: SPATIAL_MAPS_KEY })
    },
  })
}

export function useDeleteMap() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => spatialApi.deleteMap(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: SPATIAL_MAPS_KEY })
    },
  })
}
