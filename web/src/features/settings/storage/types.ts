import { z } from 'zod'

export const storageHealthStatusSchema = z.enum([
  'healthy',
  'warning',
  'cleaning',
  'emergency_stopped',
  'error',
])

export type StorageHealthStatus = z.infer<typeof storageHealthStatusSchema>

export const storageBackendSchema = z.enum(['local', 's3'])

export type StorageBackend = z.infer<typeof storageBackendSchema>

export const storageDeliveryModeSchema = z.enum(['proxy', 'redirect'])

export type StorageDeliveryMode = z.infer<typeof storageDeliveryModeSchema>

export const storageUsageBreakdownSchema = z.object({
  recordingsBytes: z.number(),
  snapshotsBytes: z.number(),
  exportsBytes: z.number(),
  otherBytes: z.number(),
})

export type StorageUsageBreakdown = z.infer<typeof storageUsageBreakdownSchema>

export const storageStatusSchema = z.object({
  backend: storageBackendSchema.optional(),
  mediaDirectory: z.string(),
  mountPoint: z.string(),
  fsType: z.string(),
  totalBytes: z.number(),
  usedBytes: z.number(),
  freeBytes: z.number(),
  usagePercent: z.number(),
  breakdown: storageUsageBreakdownSchema,
  status: storageHealthStatusSchema,
  deviceId: z.number(),
  isExternal: z.number().or(z.boolean()).transform(Boolean),
  canWrite: z.boolean(),
  remoteHealthy: z.boolean().optional(),
  remoteError: z.string().optional(),
  spoolDirectory: z.string().optional(),
  spoolUsedBytes: z.number().optional(),
  spoolFreeBytes: z.number().optional(),
  updatedAt: z.string(),
})

export type StorageStatus = z.infer<typeof storageStatusSchema>

export const s3ConfigSchema = z.object({
  endpoint: z.string(),
  region: z.string(),
  bucket: z.string(),
  prefix: z.string(),
  usePathStyle: z.boolean(),
  useTls: z.boolean(),
  insecureSkipVerify: z.boolean(),
  accessKeyId: z.string(),
  secretAccessKey: z.string().optional(),
  hasSecretAccessKey: z.boolean(),
  quotaGb: z.number(),
})

export type S3Config = z.infer<typeof s3ConfigSchema>

export interface S3ConfigPayload {
  endpoint: string
  region: string
  bucket: string
  prefix: string
  usePathStyle: boolean
  useTls: boolean
  insecureSkipVerify: boolean
  accessKeyId: string
  secretAccessKey?: string
  hasSecretAccessKey?: boolean
  quotaGb: number
}

export const storageConfigSchema = z.object({
  backend: storageBackendSchema.optional(),
  deliveryMode: storageDeliveryModeSchema.optional(),
  localFallback: z.boolean().optional(),
  mediaDirectory: z.string(),
  spoolDirectory: z.string().optional(),
  spoolMaxMb: z.number().optional(),
  recordingsRetentionDays: z.number(),
  snapshotsRetentionDays: z.number(),
  exportsRetentionHours: z.number(),
  highWatermarkPercent: z.number(),
  lowWatermarkPercent: z.number(),
  emergencyStopPercent: z.number(),
  emergencyStopMinMb: z.number(),
  s3: s3ConfigSchema.optional(),
})

export type StorageConfig = z.infer<typeof storageConfigSchema>

export interface UpdateConfigPayload {
  backend?: StorageBackend
  deliveryMode?: StorageDeliveryMode
  localFallback?: boolean
  mediaDirectory: string
  spoolDirectory?: string
  spoolMaxMb?: number
  recordingsRetentionDays: number
  snapshotsRetentionDays: number
  exportsRetentionHours: number
  highWatermarkPercent: number
  lowWatermarkPercent: number
  emergencyStopPercent: number
  emergencyStopMinMb: number
  s3?: S3ConfigPayload
}

export interface PathTestPayload {
  path: string
}

export const pathTestResponseSchema = z.object({
  path: z.string(),
  exists: z.boolean(),
  writable: z.boolean(),
  isMount: z.boolean(),
  mountPoint: z.string(),
  fsType: z.string(),
  totalBytes: z.number(),
  freeBytes: z.number(),
  deviceId: z.number(),
  isExternal: z.boolean(),
  errorReason: z.string().optional(),
})

export type PathTestResponse = z.infer<typeof pathTestResponseSchema>

export interface ConnectionTestPayload {
  backend: StorageBackend
  path?: string
  s3?: S3ConfigPayload
}

export const connectionTestResponseSchema = z.object({
  backend: storageBackendSchema,
  reachable: z.boolean(),
  writable: z.boolean(),
  errorReason: z.string().optional(),
  mountPoint: z.string().optional(),
  fsType: z.string().optional(),
  totalBytes: z.number().optional(),
  freeBytes: z.number().optional(),
  deviceId: z.number().optional(),
  isExternal: z.boolean().optional(),
  isMount: z.boolean().optional(),
})

export type ConnectionTestResponse = z.infer<
  typeof connectionTestResponseSchema
>

export const cleanupSummarySchema = z.object({
  triggerReason: z.string(),
  deletedFiles: z.number(),
  freedBytes: z.number(),
  protectedFiles: z.number().optional(),
  protectedBytes: z.number().optional(),
  durationMs: z.number(),
  targetStatus: z.string(),
  stopReason: z.string().optional(),
  finishedAt: z.string(),
})

export type CleanupSummary = z.infer<typeof cleanupSummarySchema>

export const migrationJobStatusSchema = z.enum([
  'PENDING',
  'EXTRACTING',
  'PROCESSING',
  'COMPLETED',
  'FAILED',
  'CANCELLED',
])

export type MigrationJobStatus = z.infer<typeof migrationJobStatusSchema>

export interface StartMigrationPayload {
  deleteLocal?: boolean
}

export const migrationJobResponseSchema = z.object({
  jobId: z.string(),
  status: migrationJobStatusSchema,
  progress: z.number(),
  total: z.number(),
  processed: z.number(),
  succeeded: z.number(),
  failed: z.number(),
  currentStage: z.string().optional(),
  currentFile: z.string().optional(),
  deleteLocal: z.boolean(),
  errorCode: z.string().optional(),
  errorMessage: z.string().optional(),
  createdAt: z.string(),
  updatedAt: z.string(),
  finishedAt: z.string().nullable().optional(),
})

export type MigrationJobResponse = z.infer<typeof migrationJobResponseSchema>
