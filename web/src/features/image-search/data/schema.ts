import { z } from 'zod'

export const imageSearchMatchInfoSchema = z.object({
  personId: z.string(),
  personExternalId: z.string().nullable().optional(),
  personName: z.string(),
  matchedFaceId: z.string(),
  similarity: z.number(),
})

export type ImageSearchMatchInfo = z.infer<typeof imageSearchMatchInfoSchema>

export const imageSearchResponseSchema = z.object({
  matches: z.array(imageSearchMatchInfoSchema),
})

export type ImageSearchResponse = z.infer<typeof imageSearchResponseSchema>

export interface ImageSearchParams {
  image: File
  threshold?: number
  topK?: number
  onUploadProgress?: (progressEvent: { loaded: number; total?: number }) => void
}
