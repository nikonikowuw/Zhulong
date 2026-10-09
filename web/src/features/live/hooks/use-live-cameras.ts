import { useQuery } from '@tanstack/react-query'
import { fetchLiveCameras } from '../api/live-camera-api'

export function useLiveCameras() {
  return useQuery({
    queryKey: ['live', 'cameras'],
    queryFn: fetchLiveCameras,
    refetchInterval: 10000, // 每 10 秒刷新一次相机在线状态与流信息
    retry: 1,
    staleTime: 5000,
  })
}
