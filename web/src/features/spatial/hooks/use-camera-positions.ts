import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { spatialApi } from '../api/spatial-api'
import { type CameraPositionItem } from '../data/schema'

export function spatialPositionsKey(mapId?: string | null) {
  return ['spatial', 'positions', mapId ?? 'none']
}

export function useCameraPositions(mapId?: string | null) {
  return useQuery({
    queryKey: spatialPositionsKey(mapId),
    queryFn: () =>
      mapId ? spatialApi.getPositions(mapId) : Promise.resolve([]),
    enabled: Boolean(mapId),
  })
}

export function useSaveCameraPositions() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({
      mapId,
      positions,
    }: {
      mapId: string
      positions: CameraPositionItem[]
    }) => spatialApi.savePositions(mapId, positions),
    onSuccess: (_, { mapId }) => {
      void queryClient.invalidateQueries({
        queryKey: spatialPositionsKey(mapId),
      })
    },
  })
}
