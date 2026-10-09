import { type ReactElement } from 'react'
import { useNavigate, useLocation } from '@tanstack/react-router'
import { useAuthStore } from '@/stores/auth-store'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { authApi } from '@/features/auth/api/auth-api'

interface SignOutDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function SignOutDialog({
  open,
  onOpenChange,
}: SignOutDialogProps): ReactElement {
  const navigate = useNavigate()
  const location = useLocation()
  const { auth } = useAuthStore()

  function handleSignOut(): void {
    authApi.logout().catch(() => {})
    auth.reset()
    // Preserve current location for redirect after sign-in
    const currentPath = location.href
    navigate({
      to: '/sign-in',
      search: { redirect: currentPath },
      replace: true,
    })
  }

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title='Sign out'
      desc='Are you sure you want to sign out? You will need to sign in again to access your account.'
      confirmText='Sign out'
      destructive
      handleConfirm={handleSignOut}
      className='sm:max-w-sm'
    />
  )
}
