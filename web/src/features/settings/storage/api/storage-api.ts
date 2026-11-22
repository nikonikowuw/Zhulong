import { apiClient, type ApiResponse } from '@/lib/api-client'
import {
  cleanupSummarySchema,
  connectionTestResponseSchema,
  migrationJobResponseSchema,
  pathTestResponseSchema,
  storageConfigSchema,
  storageStatusSchema,
  type CleanupSummary,
  type ConnectionTestPayload,
  type ConnectionTestResponse,
  type MigrationJobResponse,
  type PathTestPayload,
  type PathTestResponse,
  type StartMigrationPayload,
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
    return storageStatusSchema.parse(res.data.data)
  },

  /**
   * 获取当前存储策略配置（后端驱动、路径、S3 参数、保留天数、高低水位阈值）
   */
  async getConfig(): Promise<StorageConfig> {
    const res = await apiClient.get<ApiResponse<StorageConfig>>(
      '/system/storage/config'
    )
    return storageConfigSchema.parse(res.data.data)
  },

  /**
   * 保存并更新存储策略（支持本地目录热切换与 LocalFS/S3 后端切换）
   */
  async updateConfig(payload: UpdateConfigPayload): Promise<StorageStatus> {
    const res = await apiClient.put<ApiResponse<StorageStatus>>(
      '/system/storage/config',
      payload
    )
    return storageStatusSchema.parse(res.data.data)
  },

  /**
   * 预检候选本地路径有效性、写权限与外挂盘属性
   */
  async testPath(payload: PathTestPayload): Promise<PathTestResponse> {
    const res = await apiClient.post<ApiResponse<PathTestResponse>>(
      '/system/storage/test',
      payload
    )
    return pathTestResponseSchema.parse(res.data.data)
  },

  /**
   * 测试本地路径或 MinIO/S3 对象存储连通性与写入权限
   */
  async testConnection(
    payload: ConnectionTestPayload
  ): Promise<ConnectionTestResponse> {
    const res = await apiClient.post<ApiResponse<ConnectionTestResponse>>(
      '/system/storage/test-connection',
      payload
    )
    return connectionTestResponseSchema.parse(res.data.data)
  },

  /**
   * 手动立即触发一轮空间清理
   */
  async triggerCleanup(): Promise<CleanupSummary> {
    const res = await apiClient.post<ApiResponse<CleanupSummary>>(
      '/system/storage/cleanup'
    )
    return cleanupSummarySchema.parse(res.data.data)
  },

  /**
   * 启动本地历史媒体资产向远端 S3 的异步迁移任务
   */
  async startMigration(
    payload: StartMigrationPayload = {}
  ): Promise<MigrationJobResponse> {
    const res = await apiClient.post<ApiResponse<MigrationJobResponse>>(
      '/system/storage/migrate',
      payload
    )
    return migrationJobResponseSchema.parse(res.data.data)
  },

  /**
   * 查询最近一次存储迁移任务状态（无记录时返回 null）
   */
  async getLatestMigration(): Promise<MigrationJobResponse | null> {
    const res = await apiClient.get<ApiResponse<MigrationJobResponse | null>>(
      '/system/storage/migrate/latest'
    )
    if (res.data.data === null || res.data.data === undefined) {
      return null
    }
    return migrationJobResponseSchema.parse(res.data.data)
  },
}
