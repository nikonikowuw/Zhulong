import { create } from 'zustand'
import { getCookie, setCookie, removeCookie } from '@/lib/cookies'
import { authApi, type AuthUser } from '@/features/auth/api/auth-api'

const ACCESS_TOKEN_COOKIE = 'zhulong_access_token'
const USER_LOCAL_STORAGE = 'zhulong_user'

interface AuthState {
  auth: {
    user: AuthUser | null
    setUser: (user: AuthUser | null) => void
    accessToken: string
    setAccessToken: (accessToken: string) => void
    resetAccessToken: () => void
    reset: () => void
    checkAuth: () => Promise<AuthUser | null>
  }
}

function safeParseToken(raw: string | undefined): string {
  if (!raw) return ''
  try {
    return JSON.parse(raw) as string
  } catch {
    return raw
  }
}

function loadInitialUser(): AuthUser | null {
  try {
    const raw = localStorage.getItem(USER_LOCAL_STORAGE)
    if (!raw) return null
    return JSON.parse(raw) as AuthUser
  } catch {
    return null
  }
}

export const useAuthStore = create<AuthState>()((set, get) => {
  const cookieState = getCookie(ACCESS_TOKEN_COOKIE)
  const initToken = safeParseToken(cookieState)

  return {
    auth: {
      user: loadInitialUser(),
      setUser: (user) => {
        if (user) {
          localStorage.setItem(USER_LOCAL_STORAGE, JSON.stringify(user))
        } else {
          localStorage.removeItem(USER_LOCAL_STORAGE)
        }
        set((state) => ({ ...state, auth: { ...state.auth, user } }))
      },
      accessToken: initToken,
      setAccessToken: (accessToken) =>
        set((state) => {
          setCookie(ACCESS_TOKEN_COOKIE, JSON.stringify(accessToken))
          return { ...state, auth: { ...state.auth, accessToken } }
        }),
      resetAccessToken: () =>
        set((state) => {
          removeCookie(ACCESS_TOKEN_COOKIE)
          return { ...state, auth: { ...state.auth, accessToken: '' } }
        }),
      reset: () => {
        localStorage.removeItem(USER_LOCAL_STORAGE)
        removeCookie(ACCESS_TOKEN_COOKIE)
        set((state) => ({
          ...state,
          auth: { ...state.auth, user: null, accessToken: '' },
        }))
      },
      checkAuth: async () => {
        try {
          const user = await authApi.getMe()
          get().auth.setUser(user)
          return user
        } catch {
          get().auth.reset()
          return null
        }
      },
    },
  }
})
