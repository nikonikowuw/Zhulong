import { apiClient, type ApiResponse } from '@/lib/api-client'
import {
  systemHardwareResponseSchema,
  type SystemHardwareResponse,
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
}
