import { apiClient, type ApiResponse } from '@/lib/api-client'

export interface InterfaceInfo {
  name: string
  mac: string
  linkUp: boolean
  mode: 'dhcp' | 'static'
  ipAddresses: string[]
  gateway: string
  dns: string[]
  isDefaultGw: boolean
  isCurrent: boolean
}

export interface InterfaceConfigPayload {
  mode: 'dhcp' | 'static'
  ipAddress?: string
  subnetMask?: string
  gateway?: string
  dns?: string[]
  setDefault?: boolean
}

export interface ApplyResponse {
  transactionId: string
  timeoutSec: number
  targetUrl: string
  confirmToken: string
}

export interface TransactionState {
  transactionId: string
  status: 'idle' | 'pending_confirm' | 'rolling_back'
  interfaceName: string
  confirmToken: string
  targetUrl: string
  timeoutSec: number
  expiresAt: string
  rollbackConfig?: InterfaceConfigPayload
}

export interface PingResult {
  reachable: boolean
  rttMs: number
}

export const networkApi = {
  /**
   * 获取所有物理网卡列表与当前状态
   */
  async getInterfaces(): Promise<InterfaceInfo[]> {
    const res = await apiClient.get<ApiResponse<InterfaceInfo[]>>(
      '/system/network/interfaces'
    )
    return res.data.data
  },

  /**
   * 下发网卡配置，启动两阶段看门狗事务
   */
  async applyConfig(
    name: string,
    payload: InterfaceConfigPayload
  ): Promise<ApplyResponse> {
    const res = await apiClient.post<ApiResponse<ApplyResponse>>(
      `/system/network/interfaces/${name}/apply`,
      payload
    )
    return res.data.data
  },

  /**
   * 确认网络试运行配置（固化配置并解除看门狗）
   */
  async confirm(token?: string): Promise<{ status: string }> {
    const res = await apiClient.post<ApiResponse<{ status: string }>>(
      '/system/network/confirm',
      token ? { token } : {},
      {
        params: token ? { token } : undefined,
      }
    )
    return res.data.data
  },

  /**
   * 立即中止试运行并紧急回滚网络配置
   */
  async rollback(token?: string): Promise<{ status: string }> {
    const res = await apiClient.post<ApiResponse<{ status: string }>>(
      '/system/network/rollback',
      token ? { token } : {},
      {
        params: token ? { token } : undefined,
      }
    )
    return res.data.data
  },

  /**
   * 查询当前活跃的看门狗事务状态（支持刷新恢复）
   */
  async getStatus(): Promise<TransactionState> {
    const res = await apiClient.get<ApiResponse<TransactionState>>(
      '/system/network/status'
    )
    return res.data.data
  },

  /**
   * 连通性测试 (Ping)
   */
  async ping(target: string): Promise<PingResult> {
    const res = await apiClient.post<ApiResponse<PingResult>>(
      '/system/network/ping',
      { target }
    )
    return res.data.data
  },
}
