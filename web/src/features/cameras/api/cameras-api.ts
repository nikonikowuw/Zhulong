import { apiClient, type ApiResponse } from '@/lib/api-client'
import {
  cameraCredentialsResponseSchema,
  cameraListResponseSchema,
  cameraSchema,
  deleteCameraResponseSchema,
  diagnoseResponseSchema,
  type Camera,
  type CameraCredentialsResponse,
  type CameraListResponse,
  type DeleteCameraResponse,
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

/** 后端单页返回条目的硬上限（`httputil.PaginationQuery.Normalize`） */
const CAMERA_FETCH_PAGE_SIZE = 100

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
    const raw = resp.data.data ?? {
      items: [],
      total: 0,
      page: params?.page ?? 1,
      pageSize: params?.pageSize ?? 10,
    }
    return cameraListResponseSchema.parse(raw)
  },

  /**
   * 获取单台摄像机详情
   */
  async getCamera(id: string): Promise<Camera> {
    const resp = await apiClient.get<ApiResponse<Camera>>(`/cameras/${id}`)
    return cameraSchema.parse(resp.data.data)
  },

  /**
   * 新增摄像机（后端触发 3~5 秒原子流探测）
   */
  async createCamera(data: CreateCameraDto): Promise<Camera> {
    const resp = await apiClient.post<ApiResponse<Camera>>('/cameras', data)
    return cameraSchema.parse(resp.data.data)
  },

  /**
   * 更新摄像机（携带 CAS revision，可更新配置或快捷启停）
   */
  async updateCamera(id: string, data: UpdateCameraDto): Promise<Camera> {
    const resp = await apiClient.put<ApiResponse<Camera>>(
      `/cameras/${id}`,
      data
    )
    return cameraSchema.parse(resp.data.data)
  },

  /**
   * 删除摄像机
   */
  async deleteCamera(id: string): Promise<DeleteCameraResponse> {
    const resp = await apiClient.delete<ApiResponse<DeleteCameraResponse>>(
      `/cameras/${id}`
    )
    return deleteCameraResponseSchema.parse(resp.data.data)
  },

  /**
   * 获取解密后的明文凭据与流地址（仅限管理员查看与调试）
   */
  async getCredentials(id: string): Promise<CameraCredentialsResponse> {
    const resp = await apiClient.get<ApiResponse<CameraCredentialsResponse>>(
      `/cameras/${id}/credentials`
    )
    return cameraCredentialsResponseSchema.parse(resp.data.data)
  },

  /**
   * 对指定摄像机发起即时连通性与流规格探测诊断
   */
  async diagnose(id: string): Promise<DiagnoseResponse> {
    const resp = await apiClient.post<ApiResponse<DiagnoseResponse>>(
      `/cameras/${id}/diagnose`
    )
    return diagnoseResponseSchema.parse(resp.data.data)
  },

  /**
   * 拉取全量摄像机（内部按后端分页上限循环）
   *
   * 后端默认 pageSize 为 20、单页上限 100，而 `MaxCameraCapacity` 为 128，
   * 因此无法用一次请求拿到全部设备。实时预览页侧栏需要完整的设备树，
   * 不能默默丢掉第二页的设备。
   */
  async getAll(): Promise<CameraListResponse> {
    const first = await camerasApi.getCameras({
      page: 1,
      pageSize: CAMERA_FETCH_PAGE_SIZE,
    })
    if (first.items.length >= first.total) return first

    const items = [...first.items]
    const pageCount = Math.ceil(first.total / CAMERA_FETCH_PAGE_SIZE)
    for (let page = 2; page <= pageCount; page += 1) {
      const next = await camerasApi.getCameras({
        page,
        pageSize: CAMERA_FETCH_PAGE_SIZE,
      })
      items.push(...next.items)
    }

    return { ...first, items, total: first.total }
  },
}
