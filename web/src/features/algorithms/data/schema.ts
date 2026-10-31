import { z } from 'zod'

/**
 * 算法运行状态枚举
 */
export const algorithmStatusSchema = z.enum([
  'ACTIVE',
  'DISABLED',
  'STARTING',
  'RUNNING',
  'CRASHED',
  'CRASH_LOOP',
  'ROLLING_BACK',
])

export type AlgorithmStatus = z.infer<typeof algorithmStatusSchema>

/**
 * 算法任务类型枚举
 */
export const algorithmTaskTypeSchema = z.enum([
  'detection',
  'classification',
  'person_structure',
  'vehicle_structure',
])

export type AlgorithmTaskType = z.infer<typeof algorithmTaskTypeSchema>

/**
 * 算法分类枚举
 */
export const algorithmCategorySchema = z.enum([
  'safety',
  'security',
  'fire',
  'traffic',
  'general',
])

export type AlgorithmCategory = z.infer<typeof algorithmCategorySchema>

/**
 * 算法包领域实体契约
 */
export const algorithmSchema = z.object({
  id: z.string(),
  name: z.string(),
  version: z.string(),
  taskType: algorithmTaskTypeSchema,
  category: algorithmCategorySchema,
  targetArch: z.string(),
  targetDevice: z.string(),
  computeCost: z.number().int().default(10),
  memoryMb: z.number().int().default(64),
  description: z.string().default(''),
  manifestJson: z.string().default('{}'),
  configSchemaJson: z.string().default('{}'),
  status: algorithmStatusSchema.default('DISABLED'),
  previousVersion: z.string().default(''),
  createdAt: z.string(),
  updatedAt: z.string(),
})

export type Algorithm = z.infer<typeof algorithmSchema>

/**
 * 算法包列表响应契约
 */
export const algorithmListResponseSchema = z.array(algorithmSchema)

export type AlgorithmListResponse = z.infer<typeof algorithmListResponseSchema>

/**
 * 动作型端点（`POST /algorithms/:id/enable|disable|reset`）的响应契约。
 *
 * 这些端点只回 `{ status }` 而**不是**完整算法实体，因此严禁复用
 * `algorithmSchema` 去 parse：字段缺失会抛 `ZodError`，它既不是 `AxiosError`
 * 也没有 `response.data.message`，最终被全局 `MutationCache` 兜底成
 * `Something went wrong!`，同时 `onSuccess` 永不触发（列表不失效刷新）。
 */
export const algorithmActionResponseSchema = z.object({
  status: algorithmStatusSchema,
})

export type AlgorithmActionResponse = z.infer<
  typeof algorithmActionResponseSchema
>

/**
 * 卸载影响面：算法包被删除前会失去什么。
 *
 * ⚠️ 语义要点（文案必须准确，否则会误导运维）：
 * - 历史证据**不会**被删除。卸载只把 `algorithmId` 置空，
 *   `algorithmName` / `algorithmVersion` 快照仍在（迁移 000015/000016）。
 *   失去的是「按算法筛选」的能力。
 * - 绑定的任务条目会被删除，但任务本身保留（其他算法不受影响）。
 */
export const deletionImpactSchema = z.object({
  algorithmId: z.string(),
  algorithmName: z.string(),
  version: z.string(),
  status: z.string(),
  boundTaskCount: z.number().int().nonnegative().default(0),
  // 后端保证是数组而非 null，用 default([]) 兜住更早的固件
  affectedCameraIds: z.array(z.string()).default([]),
  recognitionCount: z.number().int().nonnegative().default(0),
  alarmCount: z.number().int().nonnegative().default(0),
  captureCount: z.number().int().nonnegative().default(0),
  requiresStop: z.boolean().default(false),
})

export type DeletionImpact = z.infer<typeof deletionImpactSchema>

/**
 * 试跑结果归一化坐标框 [x1, y1, x2, y2]，取值 [0.0, 1.0]
 */
export const normalizedBoxSchema = z.tuple([
  z.number(),
  z.number(),
  z.number(),
  z.number(),
])

export const detectionItemSchema = z.object({
  box: normalizedBoxSchema,
  label: z.string(),
  score: z.number(),
  attributes: z.record(z.string(), z.unknown()).optional(),
})

export const classificationItemSchema = z.object({
  label: z.string(),
  score: z.number(),
})

export const faceSubItemSchema = z.object({
  box: normalizedBoxSchema,
  quality: z.number().default(0),
  landmarks: z.array(z.tuple([z.number(), z.number()])).default([]),
  matched: z.boolean().default(false),
  person_name: z.string().optional(),
  similarity: z.number().default(0),
})

export const personItemSchema = z.object({
  track_id: z.number().default(0),
  body_box: normalizedBoxSchema,
  body_score: z.number(),
  faces: z.array(faceSubItemSchema).default([]),
  attributes: z.record(z.string(), z.unknown()).optional(),
})

export const plateSubItemSchema = z.object({
  box: normalizedBoxSchema,
  score: z.number(),
  plate_number: z.string(),
  plate_color: z.string().default(''),
  plate_type: z.number().default(0),
})

export const vehicleItemSchema = z.object({
  track_id: z.number().default(0),
  vehicle_box: normalizedBoxSchema,
  vehicle_score: z.number(),
  vehicle_type: z.string().default(''),
  vehicle_color: z.string().default(''),
  plates: z.array(plateSubItemSchema).default([]),
  attributes: z.record(z.string(), z.unknown()).optional(),
})

export const algorithmTrialResultSchema = z.object({
  costMs: z.number(),
  width: z.number(),
  height: z.number(),
  taskType: z.string(),
  count: z.number(),
  detections: z.array(detectionItemSchema).default([]),
  classifications: z.array(classificationItemSchema).default([]),
  persons: z.array(personItemSchema).default([]),
  vehicles: z.array(vehicleItemSchema).default([]),
})

export type AlgorithmTrialResult = z.infer<typeof algorithmTrialResultSchema>
export type DetectionItem = z.infer<typeof detectionItemSchema>
export type ClassificationItem = z.infer<typeof classificationItemSchema>
export type PersonItem = z.infer<typeof personItemSchema>
export type VehicleItem = z.infer<typeof vehicleItemSchema>
