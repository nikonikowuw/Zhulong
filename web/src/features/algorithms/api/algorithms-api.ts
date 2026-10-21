import { apiClient, type ApiResponse } from '@/lib/api-client'
import {
  algorithmActionResponseSchema,
  algorithmListResponseSchema,
  algorithmSchema,
  deletionImpactSchema,
  type Algorithm,
  type AlgorithmStatus,
  type DeletionImpact,
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
   * 查询卸载影响面（只读，无副作用）。
   *
   * 必须在用户确认卸载**之前**调用：它告诉用户会解绑几路任务、多少条
   * 证据会失去算法归属。不要用 DELETE 的 409 载荷来替代 —— 那样「预览」
   * 与「执行」会混在一个非幂等方法里。
   */
  async getDeletionImpact(id: string): Promise<DeletionImpact> {
    const resp = await apiClient.get<ApiResponse<unknown>>(
      `/algorithms/${id}/deletion-impact`
    )
    return deletionImpactSchema.parse(resp.data.data)
  },

  /**
   * 卸载删除算法包
   */
  async deleteAlgorithm(id: string): Promise<void> {
    await apiClient.delete(`/algorithms/${id}`)
  },

  /**
   * 启用算法。
   *
   * 后端只回 `{ status }`（见 handler.Enable），不是完整算法实体；
   * 用 `algorithmSchema` parse 会抛 ZodError 并污染全局 Toast。
   */
  async enableAlgorithm(id: string): Promise<AlgorithmStatus> {
    const resp = await apiClient.post<ApiResponse<unknown>>(
      `/algorithms/${id}/enable`
    )
    return algorithmActionResponseSchema.parse(resp.data.data).status
  },

  /**
   * 停用算法。响应契约同 {@link enableAlgorithm}。
   */
  async disableAlgorithm(id: string): Promise<AlgorithmStatus> {
    const resp = await apiClient.post<ApiResponse<unknown>>(
      `/algorithms/${id}/disable`
    )
    return algorithmActionResponseSchema.parse(resp.data.data).status
  },

  /**
   * 重置熔断。
   *
   * 仅解除 CRASH_LOOP 熔断器并把状态置回 DISABLED，**不会**重启算法；
   * 算法不处于熔断状态时后端返回 409 `ALGORITHM_NOT_IN_CRASH_LOOP`。
   */
  async resetAlgorithm(id: string): Promise<AlgorithmStatus> {
    const resp = await apiClient.post<ApiResponse<unknown>>(
      `/algorithms/${id}/reset`
    )
    return algorithmActionResponseSchema.parse(resp.data.data).status
  },
}
