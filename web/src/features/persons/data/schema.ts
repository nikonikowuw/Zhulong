import { z } from 'zod'

/**
 * 后端分页硬上限（Go 侧 `httputil.MaxPageSize`）。
 *
 * 放在契约模块而不是 api 模块：调用方的分页拉全逻辑与请求构造都要用，而 api
 * 模块在测试里会被整体 mock，常量被一并抹掉后会变成导入期语义错误。
 */
export const PERSONS_MAX_PAGE_SIZE = 100

export const faceSchema = z.object({
  id: z.string(),
  ownerRowId: z.string(),
  imagePath: z.string(),
  createdAt: z.string(),
})

export type Face = z.infer<typeof faceSchema>

export const personSchema = z.object({
  id: z.string(),
  personId: z.string().nullable().optional(),
  name: z.string(),
  source: z.enum(['LOCAL', 'API']).default('LOCAL'),
  sourceName: z.string().default(''),
  primaryFaceId: z.string().nullable().optional(),
  createdAt: z.string(),
  updatedAt: z.string(),
  faces: z.array(faceSchema).optional().default([]),
})

export type Person = z.infer<typeof personSchema>

const personImportIssueSchema = z.object({
  field: z.string().optional(),
  code: z.string(),
})

export const personImportRowSchema = z.object({
  line: z.number(),
  name: z.string(),
  personId: z.string().optional(),
  canImport: z.boolean(),
  imported: z.boolean().optional(),
  issues: z.array(personImportIssueSchema),
})

export type PersonImportRow = z.infer<typeof personImportRowSchema>

export const personImportPreviewResponseSchema = z.object({
  items: z.array(personImportRowSchema),
  total: z.number(),
  importable: z.number(),
  failed: z.number(),
})

export type PersonImportPreviewResponse = z.infer<
  typeof personImportPreviewResponseSchema
>

export const personImportCommitResponseSchema = z.object({
  items: z.array(personImportRowSchema),
  total: z.number(),
  imported: z.number(),
  failed: z.number(),
})

export type PersonImportCommitResponse = z.infer<
  typeof personImportCommitResponseSchema
>

export const personListResponseSchema = z.object({
  items: z.array(personSchema),
  total: z.number(),
  page: z.number(),
  pageSize: z.number(),
})

export type PersonListResponse = z.infer<typeof personListResponseSchema>

/**
 * 人脸底库列表响应。后端 GET /persons/{id}/faces 返回分页信封，
 * 不再是裸数组，因此 items 与 total 都需要显式解析。
 */
export const faceListResponseSchema = z.object({
  items: z.array(faceSchema),
  total: z.number(),
  page: z.number(),
  pageSize: z.number(),
})

export type FaceListResponse = z.infer<typeof faceListResponseSchema>

/**
 * PUT /persons/{id}/primary-face 的请求体。
 *
 * 由 API 层在发送前 `parse` 一遍：空字符串会让后端返回 422，在客户端就拦下
 * 能把错误落到哪个字段看得更清楚。
 */
export const setPrimaryFaceSchema = z.object({
  faceId: z.string().min(1, 'validation.faceIdRequired'),
})

/** 姓名长度上限，与后端 `personNameMaxLen` 保持一致。 */
const PERSON_NAME_MAX_LENGTH = 64
/** 工号长度上限，与后端 `personExternalIDMaxLen` 保持一致。 */
const PERSON_ID_MAX_LENGTH = 64

/**
 * 人员表单的唯一 schema 定义。
 *
 * `create` / `update` 两个请求体在字段与约束上完全相同（后端 Update 也接受
 * `personId`，改值会被 409 PERSON_ID_IMMUTABLE 拒绝），因此不再维护两份手写的
 * zod 对象 —— 曾经的两份副本各自持有 `max(64)`，改一处必漂。
 *
 * `translate` 注入错误文案：表单需要当前语言的提示，测试与非 UI 解析只需要
 * 稳定的判定结果，直接把 i18n key 当 message 用即可。
 */
export function makePersonFormSchema(translate: (key: string) => string) {
  return z.object({
    name: z
      .string()
      .trim()
      .min(1, translate('validation.nameRequired'))
      .max(PERSON_NAME_MAX_LENGTH, translate('validation.nameTooLong')),
    personId: z
      .string()
      .trim()
      .max(PERSON_ID_MAX_LENGTH, translate('validation.personIdTooLong'))
      .optional(),
  })
}

export type PersonFormValues = z.infer<ReturnType<typeof makePersonFormSchema>>

/** 无 i18n 上下文的解析器（测试、外部调用方）。错误信息即文案 key。 */
export const createPersonSchema = makePersonFormSchema((key) => key)

/** @see createPersonSchema —— 同一份定义，语义上代表 Update 请求体。 */
export const updatePersonSchema = createPersonSchema

export type CreatePersonDto = z.infer<typeof createPersonSchema>
export type UpdatePersonDto = z.infer<typeof updatePersonSchema>

export const faceImportJobStatusSchema = z.enum([
  'PENDING',
  'EXTRACTING',
  'PROCESSING',
  'COMPLETED',
  'FAILED',
  'CANCELLED',
])

export type FaceImportJobStatus = z.infer<typeof faceImportJobStatusSchema>

export const faceImportJobResponseSchema = z.object({
  jobId: z.string(),
  status: faceImportJobStatusSchema,
  progress: z.number().optional().default(0),
  total: z.number(),
  processed: z.number(),
  succeeded: z.number(),
  failed: z.number(),
  currentStage: z.string().optional().default(''),
  currentFile: z.string().optional().default(''),
  // errorCode is the stable machine contract; errorMessage is already localized
  // by the backend from the Accept-Language header.
  errorCode: z.string().optional().default(''),
  errorMessage: z.string().optional().default(''),
  createdAt: z.string(),
  updatedAt: z.string(),
  finishedAt: z.string().nullable().optional(),
})

export type FaceImportJobResponse = z.infer<typeof faceImportJobResponseSchema>
