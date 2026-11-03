import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { apiTokensApi } from '../api/api-tokens-api'
import { APITokensPage } from './api-tokens-page'

describe('APITokensPage', () => {
  let queryClient: QueryClient

  beforeEach(() => {
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
      },
    })
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders token list with active count badge', async () => {
    vi.spyOn(apiTokensApi, 'listTokens').mockResolvedValueOnce([
      {
        id: 'tok_01',
        name: 'DoorAccess',
        maskedKey: 'sk-door...1234',
        createdAt: '2026-11-03T10:00:00Z',
        lastUsedAt: null,
        revokedAt: null,
      },
    ])

    const { getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <APITokensPage />
      </QueryClientProvider>
    )

    await expect.element(getByText('DoorAccess')).toBeInTheDocument()
    await expect.element(getByText('sk-door...1234')).toBeInTheDocument()
  })
})
