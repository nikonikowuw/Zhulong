export type StorageHealthStatus =
  | 'healthy'
  | 'warning'
  | 'cleaning'
  | 'emergency_stopped'
  | 'error'

export interface StorageUsageBreakdown {
  recordingsBytes: number
  snapshotsBytes: number
  exportsBytes: number
  otherBytes: number
}

export interface StorageStatus {
  mediaDirectory: string
  mountPoint: string
  fsType: string
  totalBytes: number
  usedBytes: number
  freeBytes: number
  usagePercent: number
  breakdown: StorageUsageBreakdown
  status: StorageHealthStatus
  deviceId: number
  isExternal: boolean
  canWrite: boolean
  updatedAt: string
}

export interface StorageConfig {
  mediaDirectory: string
  recordingsRetentionDays: number
  snapshotsRetentionDays: number
  exportsRetentionHours: number
  highWatermarkPercent: number
  lowWatermarkPercent: number
  emergencyStopPercent: number
  emergencyStopMinMb: number
}

export interface UpdateConfigPayload {
  mediaDirectory: string
  recordingsRetentionDays: number
  snapshotsRetentionDays: number
  exportsRetentionHours: number
  highWatermarkPercent: number
  lowWatermarkPercent: number
  emergencyStopPercent: number
  emergencyStopMinMb: number
}

export interface PathTestPayload {
  path: string
}

export interface PathTestResponse {
  path: string
  exists: boolean
  writable: boolean
  isMount: boolean
  mountPoint: string
  fsType: string
  totalBytes: number
  freeBytes: number
  deviceId: number
  isExternal: boolean
  errorReason?: string
}

export interface CleanupSummary {
  triggerReason: string
  deletedFiles: number
  freedBytes: number
  durationMs: number
  targetStatus: string
  finishedAt: string
}
