import { apiClient, type ApiResponse } from '@/lib/api-client'
import {
  canvasMapSchema,
  cameraPositionInfoSchema,
  saveCameraPositionsResponseSchema,
  trajectoryResponseSchema,
  type CameraPositionInfo,
  type CameraPositionItem,
  type CanvasMap,
  type SaveCameraPositionsResponse,
  type TrajectoryQueryParams,
  type TrajectoryResponse,
} from '../data/schema'

export const spatialApi = {
  /** 获取所有底图 */
  async getMaps(): Promise<CanvasMap[]> {
    const res = await apiClient.get<ApiResponse<unknown[]>>('/spatial/maps')
    return (res.data.data ?? []).map((item) => canvasMapSchema.parse(item))
  },

  /** 获取单个底图详情 */
  async getMap(id: string): Promise<CanvasMap> {
    const res = await apiClient.get<ApiResponse<unknown>>(`/spatial/maps/${id}`)
    return canvasMapSchema.parse(res.data.data)
  },

  /** 上传并创建新底图 */
  async uploadMap(name: string, file: File): Promise<CanvasMap> {
    const formData = new FormData()
    formData.append('name', name)
    formData.append('file', file)

    const res = await apiClient.post<ApiResponse<unknown>>(
      '/spatial/maps',
      formData,
      {
        headers: {
          'Content-Type': 'multipart/form-data',
        },
      }
    )
    return canvasMapSchema.parse(res.data.data)
  },

  /** 重命名底图（返回更新后的完整记录） */
  async updateMapName(id: string, name: string): Promise<CanvasMap> {
    const res = await apiClient.patch<ApiResponse<unknown>>(
      `/spatial/maps/${id}`,
      { name }
    )
    return canvasMapSchema.parse(res.data.data)
  },

  /** 删除底图 */
  async deleteMap(id: string): Promise<void> {
    await apiClient.delete<ApiResponse<{ id: string }>>(`/spatial/maps/${id}`)
  },

  /** 获取某底图的所有标定点位 */
  async getPositions(mapId: string): Promise<CameraPositionInfo[]> {
    const res = await apiClient.get<ApiResponse<unknown[]>>(
      `/spatial/maps/${mapId}/positions`
    )
    return (res.data.data ?? []).map((item) =>
      cameraPositionInfoSchema.parse(item)
    )
  },

  /**
   * 批量保存底图点位。
   *
   * 空数组表示「解绑该底图上的全部摄像头」；缺失 positions 字段会被后端拒绝，
   * 因此这里必须显式发送数组而不是省略字段。
   */
  async savePositions(
    mapId: string,
    positions: CameraPositionItem[]
  ): Promise<SaveCameraPositionsResponse> {
    const res = await apiClient.put<ApiResponse<unknown>>(
      `/spatial/maps/${mapId}/positions`,
      { positions }
    )
    return saveCameraPositionsResponseSchema.parse(res.data.data)
  },

  /** 查询跨镜头时空轨迹 */
  async queryTrajectory(
    params: TrajectoryQueryParams
  ): Promise<TrajectoryResponse> {
    const searchParams = new URLSearchParams({
      mapId: params.mapId,
      targetType: params.targetType,
      startTime: params.startTime,
      endTime: params.endTime,
    })
    if (params.targetType === 'vehicle' && params.plate) {
      searchParams.set('plate', params.plate)
    }
    if (params.targetType === 'person' && params.personId) {
      searchParams.set('personId', params.personId)
    }

    const res = await apiClient.get<ApiResponse<unknown>>(
      `/spatial/trajectory?${searchParams.toString()}`
    )
    return trajectoryResponseSchema.parse(res.data.data)
  },
}
