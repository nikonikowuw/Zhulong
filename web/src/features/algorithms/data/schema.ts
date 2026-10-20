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
