import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import { auditApi } from '../api/audit-api'
import {
  type AuditLogListResponse,
  type GetAuditLogsParams,
} from '../data/schema'

const AUDIT_LOGS_QUERY_KEY = ['audit-logs'] as const

/**
 * 分页拉取审计日志列表
 */
export function useAuditLogs(
  params?: GetAuditLogsParams
): UseQueryResult<AuditLogListResponse, Error> {
  return useQuery({
    queryKey: [...AUDIT_LOGS_QUERY_KEY, params],
    queryFn: () => auditApi.getAuditLogs(params),
    placeholderData: (previousData) => previousData,
  })
}
