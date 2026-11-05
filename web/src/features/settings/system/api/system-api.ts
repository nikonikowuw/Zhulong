import { apiClient, type ApiResponse } from '@/lib/api-client'
import {
  systemHardwareResponseSchema,
  systemIdentitySchema,
  type SystemHardwareResponse,
  type SystemIdentity,
} from '../types'

export const systemApi = {
  /**
   * 获取系统当前硬件遥测数据及短期历史指标
   */
  async getHardware(): Promise<SystemHardwareResponse> {
    const res =
      await apiClient.get<ApiResponse<SystemHardwareResponse>>(
        '/system/hardware'
      )
    return systemHardwareResponseSchema.parse(res.data.data)
  },

  /**
   * 读取当前主机的设备标识
   */
  async getIdentity(): Promise<SystemIdentity> {
    const res =
      await apiClient.get<ApiResponse<SystemIdentity>>('/system/identity')
    return systemIdentitySchema.parse(res.data.data)
  },

  /**
   * 修改当前主机的设备标识（保存后新推送的事件立即生效，无需重启）
   */
  async updateIdentity(deviceId: string): Promise<SystemIdentity> {
    const res = await apiClient.put<ApiResponse<SystemIdentity>>(
      '/system/identity',
      { deviceId }
    )
    return systemIdentitySchema.parse(res.data.data)
  },
}
