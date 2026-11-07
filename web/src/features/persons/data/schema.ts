import { z } from 'zod'

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

export const personImportIssueSchema = z.object({
  field: z.string().optional(),
  code: z.string(),
})

export type PersonImportIssue = z.infer<typeof personImportIssueSchema>

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

export const setPrimaryFaceSchema = z.object({
  faceId: z.string().min(1, 'validation.faceIdRequired'),
})

export type SetPrimaryFaceDto = z.infer<typeof setPrimaryFaceSchema>

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

export const createPersonSchema = z.object({
  name: z.string().trim().min(1, 'validation.nameRequired').max(64),
  personId: z.string().trim().max(64).optional().or(z.literal('')),
})

export type CreatePersonDto = z.infer<typeof createPersonSchema>

export const updatePersonSchema = z.object({
  name: z.string().trim().min(1, 'validation.nameRequired').max(64),
  personId: z.string().trim().max(64).optional().or(z.literal('')),
})

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
