import { apiClient, type ApiResponse } from '@/lib/api-client'

export interface AuthUser {
  id?: number
  username?: string
  createdAt?: string
  email?: string
  accountNo?: string
  role?: string[]
  exp?: number
}

export interface AuthStatus {
  initialized: boolean
}

export interface InitAdminPayload {
  username: string
  password: string
  confirmPassword: string
}

export interface LoginPayload {
  username: string
  password: string
}

export const authApi = {
  /**
   * 探测系统管理员是否已完成首次初始化
   */
  async getStatus(): Promise<AuthStatus> {
    const res = await apiClient.get<ApiResponse<AuthStatus>>('/auth/status')
    return res.data.data
  },

  /**
   * 首次启动向导：初始化最高权限系统管理员
   */
  async initAdmin(payload: InitAdminPayload): Promise<AuthUser> {
    const res = await apiClient.post<ApiResponse<AuthUser>>('/auth/init', payload)
    return res.data.data
  },

  /**
   * 管理员常规密码登录
   */
  async login(payload: LoginPayload): Promise<AuthUser> {
    const res = await apiClient.post<ApiResponse<AuthUser>>('/auth/login', payload)
    return res.data.data
  },

  /**
   * 注销退出当前会话
   */
  async logout(): Promise<void> {
    await apiClient.post<ApiResponse<null>>('/auth/logout')
  },

  /**
   * 获取当前已登录管理员信息（用于会话恢复）
   */
  async getMe(): Promise<AuthUser> {
    const res = await apiClient.get<ApiResponse<AuthUser>>('/auth/me')
    return res.data.data
  },
}
