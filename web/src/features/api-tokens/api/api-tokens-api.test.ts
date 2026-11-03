import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '@/lib/api-client'
import { apiTokensApi } from './api-tokens-api'

describe('apiTokensApi', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('lists active and revoked tokens with schema validation', async () => {
    vi.spyOn(apiClient, 'get').mockResolvedValueOnce({
      data: {
        code: 'OK',
        message: 'Success',
        data: {
          items: [
            {
              id: 'tok_01',
              name: 'Platform-A',
              maskedKey: 'sk-c652...60c9',
              createdAt: '2026-11-03T10:00:00Z',
              lastUsedAt: '2026-11-03T10:30:00Z',
              revokedAt: null,
            },
          ],
        },
      },
    })

    const items = await apiTokensApi.listTokens(true)
    expect(items).toHaveLength(1)
    expect(items[0].name).toBe('Platform-A')
    expect(items[0].maskedKey).toBe('sk-c652...60c9')
    expect(apiClient.get).toHaveBeenCalledWith('/api-tokens', {
      params: { includeRevoked: 'true' },
    })
  })

  it('creates token and returns raw secret', async () => {
    vi.spyOn(apiClient, 'post').mockResolvedValueOnce({
      data: {
        code: 'OK',
        message: 'Success',
        data: {
          id: 'tok_02',
          name: 'Platform-B',
          maskedKey: 'sk-9999...1111',
          createdAt: '2026-11-03T11:00:00Z',
          lastUsedAt: null,
          revokedAt: null,
          token:
            'sk-9999999999999999999999999999999999999999999999999999999999991111',
        },
      },
    })

    const res = await apiTokensApi.createToken('Platform-B')
    expect(res.id).toBe('tok_02')
    expect(res.token).toContain('sk-9999')
    expect(apiClient.post).toHaveBeenCalledWith('/api-tokens', {
      name: 'Platform-B',
    })
  })

  it('revokes token by id', async () => {
    vi.spyOn(apiClient, 'delete').mockResolvedValueOnce({
      data: {
        code: 'OK',
        message: 'Success',
        data: {
          revoked: true,
        },
      },
    })

    const revoked = await apiTokensApi.revokeToken('tok_02')
    expect(revoked).toBe(true)
    expect(apiClient.delete).toHaveBeenCalledWith('/api-tokens/tok_02')
  })
})
