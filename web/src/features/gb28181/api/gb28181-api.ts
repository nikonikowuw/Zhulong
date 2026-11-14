import { apiClient, type ApiResponse } from '@/lib/api-client'
import {
  addChannelAsCameraResponseSchema,
  gb28181ChannelListSchema,
  gb28181ConfigResponseSchema,
  gb28181DeviceListSchema,
  gb28181DeviceSchema,
  gb28181RuntimeSnapshotStatusSchema,
  syncCatalogResponseSchema,
  type AddChannelAsCameraResponse,
  type Gb28181Channel,
  type Gb28181Config,
  type Gb28181ConfigResponse,
  type Gb28181Device,
  type Gb28181RuntimeSnapshotStatus,
  type SyncCatalogResponse,
  type UpsertGb28181DeviceRequest,
} from '../data/schema'

/**
 * GB28181 管理面 REST 客户端。
 *
 * 全部响应都经 Zod 解析：这些数据来自设备注册过程写入的库表，
 * 设备自报的厂商/型号字符串不是可信输入。
 */
export const gb28181Api = {
  /** 读取已保存配置 + 本进程运行视图（含 restartRequired）。 */
  async getConfig(): Promise<Gb28181ConfigResponse> {
    const resp =
      await apiClient.get<ApiResponse<Gb28181ConfigResponse>>('/gb28181/config')
    return gb28181ConfigResponseSchema.parse(resp.data.data)
  },

  /**
   * 保存完整目标配置。
   *
   * 保存不会重配运行中的 SIP listener；响应里的 `runtime.restartRequired`
   * 才是界面可以依赖的事实。
   */
  async updateConfig(config: Gb28181Config): Promise<Gb28181ConfigResponse> {
    const resp = await apiClient.put<ApiResponse<Gb28181ConfigResponse>>(
      '/gb28181/config',
      config
    )
    return gb28181ConfigResponseSchema.parse(resp.data.data)
  },

  /** 运行态快照：在线设备、活动会话、媒体端口池占用。 */
  async getRuntime(): Promise<Gb28181RuntimeSnapshotStatus> {
    const resp =
      await apiClient.get<ApiResponse<Gb28181RuntimeSnapshotStatus>>(
        '/gb28181/runtime'
      )
    return gb28181RuntimeSnapshotStatusSchema.parse(resp.data.data)
  },

  /** 设备 allowlist。后端明确不分页（单机上限为数百台）。 */
  async listDevices(): Promise<Gb28181Device[]> {
    const resp = await apiClient.get<ApiResponse<unknown>>('/gb28181/devices')
    const parsed = gb28181DeviceListSchema.parse(
      resp.data.data ?? { items: [] }
    )
    return parsed.items
  },

  /**
   * 预置设备或更新其凭据。
   *
   * 省略 password 会保留已存密文：轮换密钥不需要（也不应该）重传名字。
   */
  async upsertDevice(
    deviceId: string,
    payload: UpsertGb28181DeviceRequest
  ): Promise<Gb28181Device> {
    const resp = await apiClient.put<ApiResponse<Gb28181Device>>(
      `/gb28181/devices/${encodeURIComponent(deviceId)}`,
      payload
    )
    return gb28181DeviceSchema.parse(resp.data.data)
  },

  /** 删除设备。若设备下仍有关联摄像机的通道，后端将拒绝删除。 */
  async deleteDevice(deviceId: string): Promise<void> {
    await apiClient.delete<ApiResponse<unknown>>(
      `/gb28181/devices/${encodeURIComponent(deviceId)}`
    )
  },

  /** 查询设备的全部通道。 */
  async listChannels(deviceId: string): Promise<Gb28181Channel[]> {
    const resp = await apiClient.get<ApiResponse<unknown>>(
      `/gb28181/devices/${encodeURIComponent(deviceId)}/channels`
    )
    const parsed = gb28181ChannelListSchema.parse(
      resp.data.data ?? { items: [] }
    )
    return parsed.items
  },

  /** 手动触发目录同步指令。 */
  async syncCatalog(deviceId: string): Promise<SyncCatalogResponse> {
    const resp = await apiClient.post<ApiResponse<SyncCatalogResponse>>(
      `/gb28181/devices/${encodeURIComponent(deviceId)}/catalog`,
      {}
    )
    return syncCatalogResponseSchema.parse(resp.data.data)
  },

  /** 将通道显式添加为系统摄像机。 */
  async addChannelAsCamera(
    deviceId: string,
    channelId: string
  ): Promise<AddChannelAsCameraResponse> {
    const resp = await apiClient.post<ApiResponse<AddChannelAsCameraResponse>>(
      `/gb28181/devices/${encodeURIComponent(deviceId)}/channels/${encodeURIComponent(channelId)}/camera`,
      {}
    )
    return addChannelAsCameraResponseSchema.parse(resp.data.data)
  },
}
