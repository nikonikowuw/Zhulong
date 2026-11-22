import { z } from 'zod'
import { type TFunction } from 'i18next'
import { type StorageConfig, type UpdateConfigPayload } from '../types'

function isValidS3Endpoint(raw: string): boolean {
  const trimmed = raw.trim()
  if (!trimmed) return false
  if (trimmed.includes('://')) {
    try {
      const parsed = new URL(trimmed)
      if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
        return false
      }
      if (!parsed.host) return false
      if (
        (parsed.pathname !== '' && parsed.pathname !== '/') ||
        parsed.search !== '' ||
        parsed.hash !== ''
      ) {
        return false
      }
      return true
    } catch {
      return false
    }
  }
  const normalized = trimmed.replace(/\/+$/, '')
  return normalized.length > 0 && !normalized.includes('/')
}

export function createStorageFormSchema(t?: TFunction) {
  const m = {
    mediaDirectory: t
      ? t('validation.mediaDirectoryRequired')
      : '请输入有效的媒体存储路径',
    endpointRequired: t
      ? t('validation.endpointRequired')
      : '请输入 S3 / MinIO Endpoint 地址',
    endpointInvalid: t
      ? t('validation.endpointInvalid')
      : 'Endpoint 必须为 host[:port] 或 http(s)://host[:port]，不能携带路径或查询串',
    bucketRequired: t ? t('validation.bucketRequired') : '请输入存储桶名称',
    accessKeyRequired: t
      ? t('validation.accessKeyRequired')
      : '请输入 Access Key ID',
    secretKeyRequired: t
      ? t('validation.secretKeyRequired')
      : '首次启用 S3 必须输入 Secret Access Key',
    spoolDirectoryRequired: t
      ? t('validation.spoolDirectoryRequired')
      : '请输入本地 Spool 暂存目录',
    spoolMaxMin: t
      ? t('validation.spoolMaxMin')
      : 'Spool 容量上限至少为 128 MB',
    spoolMaxMax: t
      ? t('validation.spoolMaxMax')
      : 'Spool 容量上限最多为 1048576 MB',
    quotaMin: t ? t('validation.quotaMin') : '远端存储桶配额不能为负数',
    daysMin: t ? t('validation.daysMin') : '保留天数至少为 1 天',
    daysMax365: t ? t('validation.daysMax365') : '保留天数最多为 365 天',
    daysMax730: t ? t('validation.daysMax730') : '保留天数最多为 730 天',
    hoursMin: t ? t('validation.hoursMin') : '保留小时至少为 1 小时',
    hoursMax168: t ? t('validation.hoursMax168') : '保留小时最多为 168 小时',
    highMin: t ? t('validation.highMin') : '高水位至少为 50%',
    highMax: t ? t('validation.highMax') : '高水位最多为 95%',
    lowMin: t ? t('validation.lowMin') : '低水位至少为 40%',
    lowMax: t ? t('validation.lowMax') : '低水位最多为 85%',
    emergencyMin: t ? t('validation.emergencyMin') : '紧急熔断阈值至少为 90%',
    emergencyMax: t ? t('validation.emergencyMax') : '紧急熔断阈值最多为 99%',
    reserveMin: t ? t('validation.reserveMin') : '紧急保留空间至少为 100 MB',
    reserveMax: t ? t('validation.reserveMax') : '紧急保留空间最多为 102400 MB',
    lowBelowHigh: t
      ? t('validation.lowBelowHigh')
      : '低水位休眠线必须严格小于高水位清理线',
    emergencyAboveHigh: t
      ? t('validation.emergencyAboveHigh')
      : '紧急熔断停录阈值不能低于高水位清理线',
  }

  return z
    .object({
      backend: z.enum(['local', 's3']),
      deliveryMode: z.enum(['proxy', 'redirect']),
      localFallback: z.boolean(),
      mediaDirectory: z.string().trim(),
      spoolDirectory: z.string().trim(),
      spoolMaxMb: z
        .number()
        .int()
        .min(128, m.spoolMaxMin)
        .max(1048576, m.spoolMaxMax),
      recordingsRetentionDays: z
        .number()
        .int()
        .min(1, m.daysMin)
        .max(365, m.daysMax365),
      snapshotsRetentionDays: z
        .number()
        .int()
        .min(1, m.daysMin)
        .max(730, m.daysMax730),
      exportsRetentionHours: z
        .number()
        .int()
        .min(1, m.hoursMin)
        .max(168, m.hoursMax168),
      highWatermarkPercent: z
        .number()
        .int()
        .min(50, m.highMin)
        .max(95, m.highMax),
      lowWatermarkPercent: z.number().int().min(40, m.lowMin).max(85, m.lowMax),
      emergencyStopPercent: z
        .number()
        .int()
        .min(90, m.emergencyMin)
        .max(99, m.emergencyMax),
      emergencyStopMinMb: z
        .number()
        .int()
        .min(100, m.reserveMin)
        .max(102400, m.reserveMax),
      s3: z.object({
        endpoint: z.string().trim(),
        region: z.string().trim(),
        bucket: z.string().trim(),
        prefix: z.string().trim(),
        usePathStyle: z.boolean(),
        useTls: z.boolean(),
        insecureSkipVerify: z.boolean(),
        accessKeyId: z.string().trim(),
        secretAccessKey: z.string(),
        hasSecretAccessKey: z.boolean(),
        quotaGb: z.number().int().min(0, m.quotaMin),
      }),
    })
    .superRefine((data, ctx) => {
      if (data.backend === 'local' && data.mediaDirectory.trim().length === 0) {
        ctx.addIssue({
          code: 'custom',
          message: m.mediaDirectory,
          path: ['mediaDirectory'],
        })
      }
      if (data.backend === 's3') {
        if (!data.s3.endpoint.trim()) {
          ctx.addIssue({
            code: 'custom',
            message: m.endpointRequired,
            path: ['s3', 'endpoint'],
          })
        } else if (!isValidS3Endpoint(data.s3.endpoint)) {
          ctx.addIssue({
            code: 'custom',
            message: m.endpointInvalid,
            path: ['s3', 'endpoint'],
          })
        }
        if (!data.s3.bucket.trim()) {
          ctx.addIssue({
            code: 'custom',
            message: m.bucketRequired,
            path: ['s3', 'bucket'],
          })
        }
        if (!data.s3.accessKeyId.trim()) {
          ctx.addIssue({
            code: 'custom',
            message: m.accessKeyRequired,
            path: ['s3', 'accessKeyId'],
          })
        }
        if (!data.s3.hasSecretAccessKey && !data.s3.secretAccessKey.trim()) {
          ctx.addIssue({
            code: 'custom',
            message: m.secretKeyRequired,
            path: ['s3', 'secretAccessKey'],
          })
        }
        if (!data.spoolDirectory.trim()) {
          ctx.addIssue({
            code: 'custom',
            message: m.spoolDirectoryRequired,
            path: ['spoolDirectory'],
          })
        }
      }
      if (data.lowWatermarkPercent >= data.highWatermarkPercent) {
        ctx.addIssue({
          code: 'custom',
          message: m.lowBelowHigh,
          path: ['lowWatermarkPercent'],
        })
      }
      if (data.emergencyStopPercent < data.highWatermarkPercent) {
        ctx.addIssue({
          code: 'custom',
          message: m.emergencyAboveHigh,
          path: ['emergencyStopPercent'],
        })
      }
    })
}

export type StorageFormValues = z.infer<
  ReturnType<typeof createStorageFormSchema>
>

export function configToFormValues(config: StorageConfig): StorageFormValues {
  return {
    backend: config.backend ?? 'local',
    deliveryMode: config.deliveryMode ?? 'proxy',
    localFallback: config.localFallback ?? true,
    mediaDirectory: config.mediaDirectory,
    spoolDirectory: config.spoolDirectory ?? 'data/spool',
    spoolMaxMb: config.spoolMaxMb ?? 4096,
    recordingsRetentionDays: config.recordingsRetentionDays,
    snapshotsRetentionDays: config.snapshotsRetentionDays,
    exportsRetentionHours: config.exportsRetentionHours,
    highWatermarkPercent: config.highWatermarkPercent,
    lowWatermarkPercent: config.lowWatermarkPercent,
    emergencyStopPercent: config.emergencyStopPercent,
    emergencyStopMinMb: config.emergencyStopMinMb,
    s3: {
      endpoint: config.s3?.endpoint ?? '',
      region: config.s3?.region ?? 'us-east-1',
      bucket: config.s3?.bucket ?? '',
      prefix: config.s3?.prefix ?? '',
      usePathStyle: config.s3?.usePathStyle ?? true,
      useTls: config.s3?.useTls ?? false,
      insecureSkipVerify: config.s3?.insecureSkipVerify ?? false,
      accessKeyId: config.s3?.accessKeyId ?? '',
      secretAccessKey: '',
      hasSecretAccessKey: config.s3?.hasSecretAccessKey ?? false,
      quotaGb: config.s3?.quotaGb ?? 0,
    },
  }
}

export function formValuesToUpdatePayload(
  values: StorageFormValues,
  initialConfig: StorageConfig
): UpdateConfigPayload {
  const s3Payload =
    values.backend === 's3'
      ? {
          endpoint: values.s3.endpoint,
          region: values.s3.region,
          bucket: values.s3.bucket,
          prefix: values.s3.prefix,
          usePathStyle: values.s3.usePathStyle,
          useTls: values.s3.useTls,
          insecureSkipVerify: values.s3.insecureSkipVerify,
          accessKeyId: values.s3.accessKeyId,
          secretAccessKey: values.s3.secretAccessKey.trim() || undefined,
          hasSecretAccessKey: values.s3.hasSecretAccessKey,
          quotaGb: values.s3.quotaGb,
        }
      : initialConfig.s3
        ? {
            ...initialConfig.s3,
            secretAccessKey: undefined,
          }
        : undefined

  return {
    backend: values.backend,
    deliveryMode: values.backend === 's3' ? values.deliveryMode : 'proxy',
    localFallback: values.localFallback,
    mediaDirectory: values.mediaDirectory,
    spoolDirectory: values.spoolDirectory,
    spoolMaxMb: values.spoolMaxMb,
    recordingsRetentionDays: values.recordingsRetentionDays,
    snapshotsRetentionDays: values.snapshotsRetentionDays,
    exportsRetentionHours: values.exportsRetentionHours,
    highWatermarkPercent: values.highWatermarkPercent,
    lowWatermarkPercent: values.lowWatermarkPercent,
    emergencyStopPercent: values.emergencyStopPercent,
    emergencyStopMinMb: values.emergencyStopMinMb,
    s3: s3Payload,
  }
}
