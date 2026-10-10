import { apiClient, type ApiResponse } from '@/lib/api-client'
import {
  type ManualTimePayload,
  type SystemTimeStatus,
  type UpdateConfigPayload,
} from '../types'

export const timeApi = {
  /**
   * 获取当前系统时间状态、对时配置与硬件 RTC 状态
   */
  async getStatus(): Promise<SystemTimeStatus> {
    const res = await apiClient.get<ApiResponse<SystemTimeStatus>>('/system/time')
    return res.data.data
  },

  /**
   * 更新系统对时配置（模式、NTP池、间隔、时区）
   */
  async updateConfig(payload: UpdateConfigPayload): Promise<SystemTimeStatus> {
    const res = await apiClient.put<ApiResponse<SystemTimeStatus>>(
      '/system/time/config',
      payload
    )
    return res.data.data
  },

  /**
   * 立即触发一次 NTP 探测与系统时钟同步
   */
  async syncNow(): Promise<SystemTimeStatus> {
    const res = await apiClient.post<ApiResponse<SystemTimeStatus>>(
      '/system/time/sync'
    )
    return res.data.data
  },

  /**
   * 手动设置系统时间或一键同步浏览器时间
   */
  async setManualTime(payload: ManualTimePayload): Promise<SystemTimeStatus> {
    const res = await apiClient.post<ApiResponse<SystemTimeStatus>>(
      '/system/time/manual',
      payload
    )
    return res.data.data
  },
}
