import { z } from "zod";
import { createPaginatedSchema } from "@/shared/api/pagination";

export const auditLogItemSchema = z.object({
  id: z.number(),
  createdAt: z.string(),
  ip: z.string(),
  username: z.string(),
  action: z.string(),
  target: z.string(),
  detail: z.string(),
  status: z.enum(["success", "failed"]).or(z.string()),
  errorMsg: z.string().optional().default(""),
});

export type AuditLogItem = z.infer<typeof auditLogItemSchema>;

export const auditLogListResponseSchema = createPaginatedSchema(auditLogItemSchema);
export type AuditLogListResponse = z.infer<typeof auditLogListResponseSchema>;

export interface AuditLogFilterParams {
  page?: number;
  pageSize?: number;
  action?: string;
  status?: string;
  startTime?: string;
  endTime?: string;
}

export const clearAuditLogsResponseSchema = z.object({
  cleared: z.number(),
});

export type ClearAuditLogsResponse = z.infer<typeof clearAuditLogsResponseSchema>;
