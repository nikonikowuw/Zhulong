import { apiClient, type ApiResponse } from '@/lib/api-client'
import {
  type CleanupSummary,
  type PathTestPayload,
  type PathTestResponse,
  type StorageConfig,
  type StorageStatus,
  type UpdateConfigPayload,
} from '../types'

export const storageApi = {
  /**
   * 获取存储当前遥测大盘（总容量、已用空间、分类细分占用、健康状态）
   */
  async getStatus(): Promise<StorageStatus> {
    const res = await apiClient.get<ApiResponse<StorageStatus>>(
      '/system/storage/status'
    )
    return res.data.data
  },

  /**
   * 获取当前存储策略配置（路径、保留天数、高低水位阈值）
   */
  async getConfig(): Promise<StorageConfig> {
    const res = await apiClient.get<ApiResponse<StorageConfig>>(
      '/system/storage/config'
    )
    return res.data.data
  },

  /**
   * 保存并更新存储策略（支持热切流与平滑过渡）
   */
  async updateConfig(payload: UpdateConfigPayload): Promise<StorageStatus> {
    const res = await apiClient.put<ApiResponse<StorageStatus>>(
      '/system/storage/config',
      payload
    )
    return res.data.data
  },

  /**
   * 预检候选路径有效性、写权限与外挂盘属性
   */
  async testPath(payload: PathTestPayload): Promise<PathTestResponse> {
    const res = await apiClient.post<ApiResponse<PathTestResponse>>(
      '/system/storage/test',
      payload
    )
    return res.data.data
  },

  /**
   * 手动立即触发一轮空间清理
   */
  async triggerCleanup(): Promise<CleanupSummary> {
    const res = await apiClient.post<ApiResponse<CleanupSummary>>(
      '/system/storage/cleanup'
    )
    return res.data.data
  },
}
