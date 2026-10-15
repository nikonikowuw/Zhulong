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
