import { apiClient, type ApiResponse } from '@/lib/api-client'
import {
  type Camera,
  type CameraCredentialsResponse,
  type CameraListResponse,
  type DiagnoseResponse,
} from '../data/schema'

export interface CreateStreamDto {
  role: 'main' | 'sub'
  protocol: string
  rtspUrl: string
  transport: 'tcp' | 'udp'
}

export interface CreateCameraDto {
  name: string
  enabled?: boolean
  mainStream: CreateStreamDto
  subStream?: CreateStreamDto
}

export interface UpdateCameraDto {
  revision: number
  name?: string
  enabled?: boolean
  mainStream?: CreateStreamDto
  subStream?: CreateStreamDto
}

export interface GetCamerasParams {
  page?: number
  pageSize?: number
  search?: string
}

export const camerasApi = {
  /**
   * 获取摄像机分页列表
   */
  async getCameras(params?: GetCamerasParams): Promise<CameraListResponse> {
    const resp = await apiClient.get<ApiResponse<CameraListResponse>>(
      '/cameras',
      {
        params: {
          page: params?.page ?? 1,
          pageSize: params?.pageSize ?? 10,
          search: params?.search,
        },
      }
    )
    return (
      resp.data.data ?? {
        items: [],
        total: 0,
        page: params?.page ?? 1,
        pageSize: params?.pageSize ?? 10,
      }
    )
  },

  /**
   * 获取单台摄像机详情
   */
  async getCamera(id: string): Promise<Camera> {
    const resp = await apiClient.get<ApiResponse<Camera>>(`/cameras/${id}`)
    return resp.data.data
  },

  /**
   * 新增摄像机（后端触发 3~5 秒原子流探测）
   */
  async createCamera(data: CreateCameraDto): Promise<Camera> {
    const resp = await apiClient.post<ApiResponse<Camera>>('/cameras', data)
    return resp.data.data
  },

  /**
   * 更新摄像机（携带 CAS revision，可更新配置或快捷启停）
   */
  async updateCamera(id: string, data: UpdateCameraDto): Promise<Camera> {
    const resp = await apiClient.put<ApiResponse<Camera>>(
      `/cameras/${id}`,
      data
    )
    return resp.data.data
  },

  /**
   * 删除摄像机
   */
  async deleteCamera(id: string): Promise<{ id: string; deleted: boolean }> {
    const resp = await apiClient.delete<
      ApiResponse<{ id: string; deleted: boolean }>
    >(`/cameras/${id}`)
    return resp.data.data
  },

  /**
   * 获取解密后的明文凭据与流地址（仅限管理员查看与调试）
   */
  async getCredentials(id: string): Promise<CameraCredentialsResponse> {
    const resp = await apiClient.get<ApiResponse<CameraCredentialsResponse>>(
      `/cameras/${id}/credentials`
    )
    return resp.data.data
  },

  /**
   * 对指定摄像机发起即时连通性与流规格探测诊断
   */
  async diagnose(id: string): Promise<DiagnoseResponse> {
    const resp = await apiClient.post<ApiResponse<DiagnoseResponse>>(
      `/cameras/${id}/diagnose`
    )
    return resp.data.data
  },
}
