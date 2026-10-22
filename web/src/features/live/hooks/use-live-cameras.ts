import { useMemo } from 'react'
import { useAllCameras, useCameraEvents } from '@/features/cameras/contract'
import { toLiveCameraItem } from '../api/live-camera-api'
import { type LiveCameraItem } from '../types'

/**
 * 实时预览页的设备树数据源
 *
 * 数据通路与「摄像机管理」页完全一致：
 * - 全量列表由 `useAllCameras` 经 TanStack Query 托管（含分页拉全）
 * - 在线/推流状态由 `useCameraEvents` 的 SSE 增量事件原地 patch 缓存，
 *   不再用 `refetchInterval` 轮询，因此状态变化是秒级而非最坏 10 秒
 * - 展示模型由 `toLiveCameraItem` 在渲染层派生，不复制到任何客户端 store
 */
export function useLiveCameras(): {
  data: LiveCameraItem[]
  isLoading: boolean
  error: Error | null
  refetch: () => void
} {
  useCameraEvents()

  const query = useAllCameras()
  const cameras = useMemo<LiveCameraItem[]>(
    () => (query.data?.items ?? []).map(toLiveCameraItem),
    [query.data]
  )

  return {
    data: cameras,
    isLoading: query.isLoading,
    error: query.error,
    refetch: () => void query.refetch(),
  }
}
