import { z } from 'zod'
import { apiClient } from '@/lib/api-client'
import {
  type AuditLogListResponse,
  auditLogListResponseSchema,
  type GetAuditLogsParams,
} from '../data/schema'

export const auditApi = {
  /**
   * 获取系统审计日志列表
   */
  async getAuditLogs(
    params?: GetAuditLogsParams
  ): Promise<AuditLogListResponse> {
    const resp = await apiClient.get<unknown>('/audit/logs', {
      params: {
        page: params?.page ?? 1,
        pageSize: params?.pageSize ?? 20,
        action: params?.action?.trim() || undefined,
        actions: params?.actions?.length ? params.actions : undefined,
        status: params?.status?.trim() || undefined,
        startTime: params?.startTime?.trim() || undefined,
        endTime: params?.endTime?.trim() || undefined,
      },
      paramsSerializer: { indexes: null },
    })

    return z
      .object({
        code: z.literal('OK'),
        message: z.string(),
        data: auditLogListResponseSchema,
      })
      .parse(resp.data).data
  },
}
