import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/stores/auth-store'

interface AccountIdentity {
  displayName: string
  displayRole: string
  /** Best-effort monogram: this host has exactly one administrator, no avatar upload. */
  initials: string
}

export function useAccountIdentity(): AccountIdentity {
  const { t } = useTranslation()
  const { auth } = useAuthStore()

  const displayName = auth.user?.username ?? t('account.fallbackName')
  const initials = Array.from(displayName).slice(0, 2).join('').toUpperCase()

  return { displayName, displayRole: t('account.role'), initials }
}
