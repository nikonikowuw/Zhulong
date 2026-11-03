import { z } from 'zod'
import { apiClient } from '@/lib/api-client'
import {
  type APIToken,
  type APITokenCreateResponse,
  apiTokenCreateResponseSchema,
  apiTokenListResponseSchema,
} from '../data/schema'

export const apiTokensApi = {
  async listTokens(includeRevoked = false): Promise<APIToken[]> {
    const resp = await apiClient.get<unknown>('/api-tokens', {
      params: { includeRevoked: includeRevoked ? 'true' : undefined },
    })
    const parsed = z
      .object({
        code: z.literal('OK'),
        data: apiTokenListResponseSchema,
      })
      .parse(resp.data)
    return parsed.data.items
  },

  async createToken(name: string): Promise<APITokenCreateResponse> {
    const resp = await apiClient.post<unknown>('/api-tokens', { name })
    const parsed = z
      .object({
        code: z.literal('OK'),
        data: apiTokenCreateResponseSchema,
      })
      .parse(resp.data)
    return parsed.data
  },

  async revokeToken(id: string): Promise<boolean> {
    const resp = await apiClient.delete<unknown>(`/api-tokens/${id}`)
    const parsed = z
      .object({
        code: z.literal('OK'),
        data: z.object({ revoked: z.boolean() }),
      })
      .parse(resp.data)
    return parsed.data.revoked
  },
}
