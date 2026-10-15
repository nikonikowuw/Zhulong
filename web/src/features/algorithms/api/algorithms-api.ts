import { apiClient, type ApiResponse } from '@/lib/api-client'
import {
  algorithmListResponseSchema,
  algorithmSchema,
  type Algorithm,
} from '../data/schema'

export interface UploadProgressCallback {
  (percent: number): void
}

export const algorithmsApi = {
  /**
   * 获取所有已安装算法包列表
   */
  async getAlgorithms(): Promise<Algorithm[]> {
    const resp = await apiClient.get<ApiResponse<unknown>>('/algorithms')
    return algorithmListResponseSchema.parse(resp.data.data ?? [])
  },

  /**
   * 获取指定算法详情
   */
  async getAlgorithm(id: string): Promise<Algorithm> {
    const resp = await apiClient.get<ApiResponse<unknown>>(`/algorithms/${id}`)
    return algorithmSchema.parse(resp.data.data)
  },

  /**
   * 上传并安装算法包 (.tar.gz 或 .zip)
   */
  async installAlgorithm(
    file: File,
    onProgress?: UploadProgressCallback
  ): Promise<Algorithm> {
    const formData = new FormData()
    formData.append('file', file)

    const resp = await apiClient.post<ApiResponse<unknown>>(
      '/algorithms/install',
      formData,
      {
        headers: {
          'Content-Type': 'multipart/form-data',
        },
        onUploadProgress: (progressEvent) => {
          if (progressEvent.total && progressEvent.total > 0 && onProgress) {
            const percent = Math.round(
              (progressEvent.loaded * 100) / progressEvent.total
            )
            onProgress(percent)
          }
        },
      }
    )
    return algorithmSchema.parse(resp.data.data)
  },

  /**
   * 卸载删除算法包
   */
  async deleteAlgorithm(id: string): Promise<void> {
    await apiClient.delete(`/algorithms/${id}`)
  },

  /**
   * 启用算法
   */
  async enableAlgorithm(id: string): Promise<Algorithm> {
    const resp = await apiClient.post<ApiResponse<unknown>>(
      `/algorithms/${id}/enable`
    )
    return algorithmSchema.parse(resp.data.data)
  },

  /**
   * 停用算法
   */
  async disableAlgorithm(id: string): Promise<Algorithm> {
    const resp = await apiClient.post<ApiResponse<unknown>>(
      `/algorithms/${id}/disable`
    )
    return algorithmSchema.parse(resp.data.data)
  },
}
