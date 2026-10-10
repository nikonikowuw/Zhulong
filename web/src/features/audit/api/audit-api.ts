import { z } from 'zod'
import { apiClient } from '@/lib/api-client'
import {
  type AuditLogListResponse,
  auditLogListResponseSchema,
  type ClearAuditLogsParams,
  type GetAuditLogsParams,
} from '../data/schema'

export interface ClearAuditLogsResponse {
  cleared: number
}

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

  /**
   * 清理系统审计日志
   */
  async clearAuditLogs(
    params?: ClearAuditLogsParams
  ): Promise<ClearAuditLogsResponse> {
    const resp = await apiClient.delete<unknown>('/audit/logs', {
      params: {
        before: params?.before?.trim() || undefined,
      },
    })

    return z
      .object({
        code: z.literal('OK'),
        message: z.string(),
        data: z.object({ cleared: z.number().int().nonnegative() }),
      })
      .parse(resp.data).data
  },
}
