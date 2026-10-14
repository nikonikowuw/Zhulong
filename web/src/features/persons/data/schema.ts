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
  personId: z.string(),
  name: z.string(),
  primaryFaceId: z.string().nullable().optional(),
  createdAt: z.string(),
  updatedAt: z.string(),
  faces: z.array(faceSchema).optional().default([]),
})

export type Person = z.infer<typeof personSchema>

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

export const createPersonSchema = z.object({
  name: z.string().trim().min(1, 'validation.nameRequired').max(64),
  personId: z
    .string()
    .trim()
    .max(64)
    .optional()
    .or(z.literal('')),
})

export type CreatePersonDto = z.infer<typeof createPersonSchema>

export const updatePersonSchema = z.object({
  name: z.string().trim().min(1, 'validation.nameRequired').max(64),
})

export type UpdatePersonDto = z.infer<typeof updatePersonSchema>

