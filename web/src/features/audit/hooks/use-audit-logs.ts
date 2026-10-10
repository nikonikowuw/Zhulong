import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseQueryResult,
} from '@tanstack/react-query'
import { auditApi, type ClearAuditLogsResponse } from '../api/audit-api'
import {
  type AuditLogListResponse,
  type ClearAuditLogsParams,
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

/**
 * 清理审计日志 Mutation
 */
export function useClearAuditLogs() {
  const queryClient = useQueryClient()

  return useMutation<
    ClearAuditLogsResponse,
    Error,
    ClearAuditLogsParams | undefined
  >({
    mutationFn: (params) => auditApi.clearAuditLogs(params),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: AUDIT_LOGS_QUERY_KEY })
    },
  })
}
