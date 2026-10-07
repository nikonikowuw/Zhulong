import type { SupportedLanguage } from "@/shared/i18n";
import { getApiData, deleteApiData } from "@/shared/api/client";
import {
  auditLogListResponseSchema,
  clearAuditLogsResponseSchema,
  type AuditLogFilterParams,
  type AuditLogListResponse,
  type ClearAuditLogsResponse,
} from "../types";

export function fetchAuditLogs(
  params: AuditLogFilterParams,
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<AuditLogListResponse> {
  const query = new URLSearchParams();
  if (params.page !== undefined) query.set("page", String(params.page));
  if (params.pageSize !== undefined) query.set("pageSize", String(params.pageSize));
  if (params.action) query.set("action", params.action);
  if (params.status) query.set("status", params.status);
  if (params.startTime) query.set("startTime", params.startTime);
  if (params.endTime) query.set("endTime", params.endTime);

  const qs = query.toString();
  const url = qs ? `/api/v1/audit/logs?${qs}` : "/api/v1/audit/logs";
  return getApiData(url, auditLogListResponseSchema, language, signal);
}

export function clearAuditLogs(
  language: SupportedLanguage,
  before?: string,
  signal?: AbortSignal,
): Promise<ClearAuditLogsResponse> {
  const url = before
    ? `/api/v1/audit/logs?before=${encodeURIComponent(before)}`
    : "/api/v1/audit/logs";
  return deleteApiData(url, clearAuditLogsResponseSchema, language, signal);
}
