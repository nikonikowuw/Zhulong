import { clearCookies } from '@/test-utils/cookies'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { authApi } from '@/features/auth/api/auth-api'

async function importAuthStore() {
  const { useAuthStore } = await import('./auth-store')
  return useAuthStore
}

const sampleUser = {
  id: 1,
  username: 'admin',
  createdAt: '2026-10-09T08:00:00Z',
}

describe('useAuthStore', () => {
  beforeEach(() => {
    clearCookies()
    localStorage.clear()
    vi.resetModules()
    vi.restoreAllMocks()
  })

  it('starts with null user when nothing is persisted', async () => {
    const useAuthStore = await importAuthStore()

    expect(useAuthStore.getState().auth.user).toBeNull()
    expect(useAuthStore.getState().auth.accessToken).toBe('')
  })

  it('updates the signed-in user via setUser and persists to localStorage', async () => {
    const useAuthStore = await importAuthStore()

    useAuthStore.getState().auth.setUser({ ...sampleUser })
    expect(useAuthStore.getState().auth.user).toEqual(sampleUser)

    vi.resetModules()
    const useAuthStoreAfterReload = await importAuthStore()
    expect(useAuthStoreAfterReload.getState().auth.user).toEqual(sampleUser)
  })

  it('reset clears user and access token and drops persistence', async () => {
    const useAuthStore = await importAuthStore()
    useAuthStore.getState().auth.setUser({ ...sampleUser })
    useAuthStore.getState().auth.setAccessToken('test-token')

    useAuthStore.getState().auth.reset()

    expect(useAuthStore.getState().auth.user).toBeNull()
    expect(useAuthStore.getState().auth.accessToken).toBe('')

    vi.resetModules()
    const useAuthStoreAfterReload = await importAuthStore()
    expect(useAuthStoreAfterReload.getState().auth.user).toBeNull()
  })

  it('checkAuth sets user on success', async () => {
    vi.spyOn(authApi, 'getMe').mockResolvedValue(sampleUser)
    const useAuthStore = await importAuthStore()

    const result = await useAuthStore.getState().auth.checkAuth()
    expect(result).toEqual(sampleUser)
    expect(useAuthStore.getState().auth.user).toEqual(sampleUser)
  })

  it('checkAuth resets user on failure', async () => {
    vi.spyOn(authApi, 'getMe').mockRejectedValue(new Error('401 Unauthorized'))
    const useAuthStore = await importAuthStore()
    useAuthStore.getState().auth.setUser(sampleUser)

    const result = await useAuthStore.getState().auth.checkAuth()
    expect(result).toBeNull()
    expect(useAuthStore.getState().auth.user).toBeNull()
  })
})
