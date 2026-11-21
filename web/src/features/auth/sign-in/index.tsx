import { type ReactElement, useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate, useSearch } from '@tanstack/react-router'
import { AlertTriangle, Lock } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/stores/auth-store'
import { STATUS_LEVEL_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { authApi, type AuthUser } from '../api/auth-api'
import { AuthLayout } from '../auth-layout'
import { LoginForm } from '../components/login-form'
import { SetupCard } from '../components/setup-card'

export function SignIn(): ReactElement {
  const navigate = useNavigate()
  const search = useSearch({ from: '/(auth)/sign-in' })
  const { auth } = useAuthStore()
  const { t } = useTranslation('auth')

  // Probe the backend system-administrator initialization status
  const {
    data: status,
    isLoading: isStatusLoading,
    isError: isStatusError,
    refetch,
  } = useQuery({
    queryKey: ['auth', 'status'],
    queryFn: () => authApi.getStatus(),
    staleTime: 10_000,
    retry: 1,
  })

  const targetPath = search.redirect || '/'

  // If already authenticated, jump straight to the redirect target or home
  useEffect(() => {
    if (auth.user) {
      navigate({
        to: targetPath,
        replace: true,
      })
    }
  }, [auth.user, navigate, targetPath])

  function handleSuccess(_user: AuthUser): void {
    navigate({
      to: targetPath,
      replace: true,
    })
  }

  // Driven purely by backend status: wizard when uninitialized, login otherwise
  const isSetupMode = Boolean(status && !status.initialized)

  function renderCard(): ReactElement {
    if (isStatusLoading) {
      /* Skeleton: shown while the first status probe is pending, avoids CLS */
      return (
        <Card className='border-border/70 bg-card/90 shadow-2xl backdrop-blur-xl'>
          <CardHeader className='space-y-2 pb-5'>
            <div className='flex items-center gap-2.5'>
              <Skeleton className='size-9 rounded-lg' />
              <div className='flex-1 space-y-1.5'>
                <Skeleton className='h-5 w-28' />
                <Skeleton className='h-3.5 w-48' />
              </div>
            </div>
          </CardHeader>
          <CardContent className='space-y-4'>
            <div className='space-y-2'>
              <Skeleton className='h-3.5 w-16' />
              <Skeleton className='h-11 w-full' />
            </div>
            <div className='space-y-2'>
              <Skeleton className='h-3.5 w-12' />
              <Skeleton className='h-11 w-full' />
            </div>
            <Skeleton className='h-11 w-full rounded-md' />
          </CardContent>
        </Card>
      )
    }

    if (isSetupMode) {
      /* First-launch setup: 强制路径，必须是整页最醒目的东西 */
      return <SetupCard onSuccess={handleSuccess} />
    }

    /* Regular administrator password login card */
    return (
      <Card className='border-border/70 bg-card/90 shadow-2xl backdrop-blur-xl'>
        <CardHeader className='space-y-1.5 pb-5'>
          <div className='flex items-center gap-2.5'>
            <div className='flex size-9 items-center justify-center rounded-lg bg-primary/10 text-primary ring-1 ring-primary/20'>
              <Lock className='size-4.5' />
            </div>
            <div>
              <CardTitle className='text-lg font-bold tracking-tight'>
                {t('login.cardTitle')}
              </CardTitle>
              <CardDescription className='text-xs'>
                {t('login.cardDescription')}
              </CardDescription>
            </div>
          </div>
        </CardHeader>
        <CardContent>
          <LoginForm onSuccess={handleSuccess} />
        </CardContent>
      </Card>
    )
  }

  return (
    <AuthLayout>
      <div className='space-y-3'>
        {/* 状态探测失败时不能装作「一切正常」：运维会以为设备已初始化、反复试密码。
            这里明说「无法确认」，并给一次就地重试。 */}
        {isStatusError ? (
          <div
            role='alert'
            className={cn(
              'flex items-center justify-between gap-2 rounded-md border px-3 py-2 text-xs',
              STATUS_LEVEL_CLASS.warning
            )}
          >
            <span className='flex min-w-0 items-center gap-1.5'>
              <AlertTriangle className='size-3.5 shrink-0' aria-hidden='true' />
              <span className='truncate'>{t('layout.statusUnknown')}</span>
            </span>
            <Button
              type='button'
              variant='outline'
              size='sm'
              className='h-8 shrink-0 px-2.5 text-xs'
              onClick={() => void refetch()}
            >
              {t('layout.statusRetry')}
            </Button>
          </div>
        ) : null}
        {renderCard()}
      </div>
    </AuthLayout>
  )
}
