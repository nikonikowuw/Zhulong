import { z } from 'zod'

export const boxSchema = z
  .tuple([z.number(), z.number(), z.number(), z.number()])
  .default([0, 0, 0, 0])

export type BoundingBox = [number, number, number, number]

export const faceCandidateSchema = z
  .object({
    personId: z.string().optional(),
    personName: z.string().optional(),
    faceId: z.string().optional(),
    similarity: z.number().default(0),
    // 兼容蛇形命名
    person_id: z.string().optional(),
    person_name: z.string().optional(),
    face_id: z.string().optional(),
  })
  .transform((data) => ({
    personId: data.personId ?? data.person_id ?? '',
    personName: data.personName ?? data.person_name ?? '',
    faceId: data.faceId ?? data.face_id ?? '',
    similarity: data.similarity,
  }))

export type FaceCandidate = z.infer<typeof faceCandidateSchema>

export const recognitionSchema = z.object({
  id: z.string(),
  cameraId: z.string(),
  algorithmId: z.string().default(''),
  // ⚠️ 算法被卸载后 algorithmId 会变成空串，但这两个快照字段仍在 ——
  // 它们才是「这批记录是哪个算法产生的」的唯一凭证。展示时优先用它们。
  // 可选：迁移 000015 之前写入的历史行没有快照值。
  algorithmName: z.string().optional(),
  algorithmVersion: z.string().optional(),
  pts: z.number().int().default(0),
  trackId: z.number().int().default(0),
  personId: z.string().nullable().default(null),
  personName: z.string().default(''),
  matchedFaceId: z.string().default(''),
  matched: z.boolean().default(false),
  similarity: z.number().default(0),
  candidates: z.array(faceCandidateSchema).default([]),
  faceImagePath: z.string().default(''),
  galleryImagePath: z.string().default(''),
  imagePath: z.string().default(''),
  box: boxSchema,
  bodyBox: boxSchema,
  attributes: z.record(z.string(), z.unknown()).default({}),
  capturedAt: z.string(),
  createdAt: z.string(),
})

export type Recognition = z.infer<typeof recognitionSchema>

export const recognitionListResponseSchema = z.object({
  items: z.array(recognitionSchema),
  total: z.number().int().nonnegative(),
  page: z.number().int().positive(),
  pageSize: z.number().int().positive(),
})

export type RecognitionListResponse = z.infer<
  typeof recognitionListResponseSchema
>

export const alarmSchema = z.object({
  id: z.string(),
  cameraId: z.string(),
  algorithmId: z.string().default(''),
  // 同 recognitionSchema：算法卸载后仍可回答「谁产生的」。
  algorithmName: z.string().optional(),
  algorithmVersion: z.string().optional(),
  alarmType: z.string(),
  score: z.number().default(0),
  box: boxSchema,
  imagePath: z.string().default(''),
  cropPath: z.string().default(''),
  extra: z.record(z.string(), z.unknown()).default({}),
  pts: z.number().int().default(0),
  capturedAt: z.string(),
  createdAt: z.string(),
})

export type Alarm = z.infer<typeof alarmSchema>

export const alarmListResponseSchema = z.object({
  items: z.array(alarmSchema),
  total: z.number().int().nonnegative(),
  page: z.number().int().positive(),
  pageSize: z.number().int().positive(),
})

export type AlarmListResponse = z.infer<typeof alarmListResponseSchema>

export const captureSchema = z.object({
  id: z.string(),
  cameraId: z.string(),
  // 与 recognitionSchema / alarmSchema 同构：卸载算法后 ID 变空，
  // 名称与版本快照仍能回答「这张抓拍是哪个算法产生的」。
  // ⚠️ algorithmId 声明为 optional 而非 default('')：迁移 000016 之前写入的
  // 历史抓拍在 JSON 里根本没有这个键，Zod 的 default 只解决运行时取值，
  // TS 推导出的类型仍是 required，会让所有测试 fixture 报缺字段。
  algorithmId: z.string().optional(),
  algorithmName: z.string().optional(),
  algorithmVersion: z.string().optional(),
  targetType: z.string(),
  trackId: z.number().int().default(0),
  score: z.number().default(0),
  box: boxSchema,
  cropPath: z.string().default(''),
  imagePath: z.string().default(''),
  attributes: z.record(z.string(), z.unknown()).default({}),
  pts: z.number().int().default(0),
  capturedAt: z.string(),
  createdAt: z.string(),
})

export type Capture = z.infer<typeof captureSchema>

export const captureListResponseSchema = z.object({
  items: z.array(captureSchema),
  total: z.number().int().nonnegative(),
  page: z.number().int().positive(),
  pageSize: z.number().int().positive(),
})

export type CaptureListResponse = z.infer<typeof captureListResponseSchema>

export type RecordItem = Recognition | Alarm | Capture

export interface GetRecognitionsParams {
  page?: number
  pageSize?: number
  cameraId?: string
  personId?: string
  matched?: boolean
  startTime?: string
  endTime?: string
}

export interface GetAlarmsParams {
  page?: number
  pageSize?: number
  cameraId?: string
  alarmType?: string
  startTime?: string
  endTime?: string
}

export interface GetCapturesParams {
  page?: number
  pageSize?: number
  cameraId?: string
  targetType?: string
  startTime?: string
  endTime?: string
}

export const recordsSearchSchema = z.object({
  tab: z.enum(['recognitions', 'alarms', 'captures']).catch('recognitions'),
  view: z.enum(['table', 'grid']).catch('table'),
  page: z.coerce.number().int().positive().catch(1).default(1),
  pageSize: z.coerce.number().int().positive().max(100).catch(20).default(20),
  cameraId: z.string().optional().catch(undefined),
  preset: z
    .enum(['15m', '30m', '1h', '6h', '24h', 'today', '7d', '30d'])
    .optional()
    .catch(undefined),
  startTime: z.string().optional().catch(undefined),
  endTime: z.string().optional().catch(undefined),
  // 识别专属
  matched: z.enum(['all', 'true', 'false']).optional().catch(undefined),
  personId: z.string().optional().catch(undefined),
  // 告警专属
  alarmType: z.string().optional().catch(undefined),
  // 抓拍专属
  targetType: z.string().optional().catch(undefined),
})

export type RecordsSearchParams = z.infer<typeof recordsSearchSchema>

/**
 * 算法的展示名称。
 *
 * ⚠️ 优先用写入时快照：算法被卸载后 `algorithmId` 会变成空串，`algorithms`
 * 表里也没有对应行，此时只有 `algorithmName` / `algorithmVersion` 能回答
 * 「这批记录是哪个算法产生的」。仅在快照缺失时（迁移前的历史行）才回退到 ID。
 */
export function algorithmLabel(record: {
  algorithmId?: string
  algorithmName?: string
  algorithmVersion?: string
}): string {
  const name = record.algorithmName?.trim()
  if (name) {
    const version = record.algorithmVersion?.trim()
    return version ? `${name} v${version}` : name
  }
  return record.algorithmId?.trim() || ''
}
