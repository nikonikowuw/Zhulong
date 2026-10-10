import { z } from 'zod'

export const auditLogSchema = z.object({
  id: z.number().int().nonnegative(),
  createdAt: z.iso.datetime({ offset: true }),
  ip: z.string(),
  username: z.string(),
  action: z.string(),
  target: z.string(),
  detail: z.string(),
  status: z.string(),
  errorMsg: z.string().default(''),
})

export type AuditLog = z.infer<typeof auditLogSchema>

export const auditLogListResponseSchema = z.object({
  items: z.array(auditLogSchema),
  total: z.number().int().nonnegative(),
  page: z.number().int().positive(),
  pageSize: z.number().int().positive(),
})

export type AuditLogListResponse = z.infer<typeof auditLogListResponseSchema>

export interface GetAuditLogsParams {
  page?: number
  pageSize?: number
  action?: string
  actions?: string[]
  status?: string
  startTime?: string
  endTime?: string
}

export interface ClearAuditLogsParams {
  before?: string
}

const statusFilterSchema = z.enum(['success', 'failed'])
const timeSearchSchema = z.iso.datetime({ offset: true })

export const auditSearchSchema = z.object({
  page: z.number().int().positive().optional().catch(1),
  pageSize: z.number().int().positive().max(100).optional().catch(20),
  action: z
    .union([z.array(z.string()), z.string()])
    .optional()
    .catch([]),
  status: z
    .union([z.array(statusFilterSchema), statusFilterSchema])
    .optional()
    .catch([]),
  preset: z
    .enum(['15m', '30m', '1h', '6h', '24h', 'today', '7d', '30d'])
    .optional()
    .catch(undefined),
  startTime: timeSearchSchema.optional().catch(undefined),
  endTime: timeSearchSchema.optional().catch(undefined),
})
