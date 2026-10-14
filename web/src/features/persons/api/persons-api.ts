import { apiClient, type ApiResponse } from '@/lib/api-client'
import {
  type CreatePersonDto,
  type Face,
  type Person,
  type PersonListResponse,
  type UpdatePersonDto,
} from '../data/schema'

export interface GetPersonsParams {
  page?: number
  pageSize?: number
  search?: string
}

export const personsApi = {
  /**
   * 获取人员分页列表
   */
  async getPersons(params?: GetPersonsParams): Promise<PersonListResponse> {
    const resp = await apiClient.get<ApiResponse<PersonListResponse>>(
      '/persons',
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
   * 获取单个人员详情
   */
  async getPerson(id: string): Promise<Person> {
    const resp = await apiClient.get<ApiResponse<Person>>(`/persons/${id}`)
    return resp.data.data
  },

  /**
   * 创建人员档案
   */
  async createPerson(data: CreatePersonDto): Promise<Person> {
    const resp = await apiClient.post<ApiResponse<Person>>('/persons', data)
    return resp.data.data
  },

  /**
   * 更新人员基本资料
   */
  async updatePerson(id: string, data: UpdatePersonDto): Promise<Person> {
    const resp = await apiClient.put<ApiResponse<Person>>(
      `/persons/${id}`,
      data
    )
    return resp.data.data
  },

  /**
   * 删除人员档案
   */
  async deletePerson(id: string): Promise<void> {
    await apiClient.delete<ApiResponse<{ deleted: boolean }>>(`/persons/${id}`)
  },

  /**
   * 获取人员关联的所有人脸底库
   */
  async listFaces(personId: string): Promise<Face[]> {
    const resp = await apiClient.get<ApiResponse<Face[]>>(
      `/persons/${personId}/faces`
    )
    return resp.data.data ?? []
  },

  /**
   * 录入新人脸底库（本地图片文件直传）
   */
  async addFace(
    personId: string,
    data: File | FormData
  ): Promise<Face> {
    const formData = data instanceof FormData ? data : new FormData()
    if (data instanceof File) {
      formData.append('file', data)
    }

    const resp = await apiClient.post<ApiResponse<Face>>(
      `/persons/${personId}/faces`,
      formData,
      {
        headers: { 'Content-Type': 'multipart/form-data' },
      }
    )
    return resp.data.data
  },

  /**
   * 获取指定人脸底库图片的只读访问 URL
   */
  getFaceImageUrl(personId: string, faceId: string): string {
    return `/api/v1/persons/${personId}/faces/${faceId}/image`
  },

  /**
   * 删除单个人脸底库
   */
  async deleteFace(personId: string, faceId: string): Promise<void> {
    await apiClient.delete<ApiResponse<{ deleted: boolean }>>(
      `/persons/${personId}/faces/${faceId}`
    )
  },
}
