import { z } from 'zod'

export const apiTokenSchema = z.object({
  id: z.string(),
  name: z.string(),
  maskedKey: z.string(),
  createdAt: z.string(),
  lastUsedAt: z.string().nullable().optional(),
  revokedAt: z.string().nullable().optional(),
})

export type APIToken = z.infer<typeof apiTokenSchema>

export const apiTokenListResponseSchema = z.object({
  items: z.array(apiTokenSchema),
})

export type APITokenListResponse = z.infer<typeof apiTokenListResponseSchema>

export const apiTokenCreateResponseSchema = z.object({
  id: z.string(),
  name: z.string(),
  maskedKey: z.string(),
  createdAt: z.string(),
  lastUsedAt: z.string().nullable().optional(),
  revokedAt: z.string().nullable().optional(),
  token: z.string(),
})

export type APITokenCreateResponse = z.infer<
  typeof apiTokenCreateResponseSchema
>
