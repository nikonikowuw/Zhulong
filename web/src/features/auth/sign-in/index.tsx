import { type ReactElement, useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate, useSearch } from '@tanstack/react-router'
import { Lock, Shield } from 'lucide-react'
import { useAuthStore } from '@/stores/auth-store'
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
import { InitForm } from '../components/init-form'
import { LoginForm } from '../components/login-form'

export function SignIn(): ReactElement {
  const navigate = useNavigate()
  const search = useSearch({ from: '/(auth)/sign-in' })
  const { auth } = useAuthStore()

  // 探测后端系统管理员初始化状态
  const { data: status, isLoading: isStatusLoading } = useQuery({
    queryKey: ['auth', 'status'],
    queryFn: () => authApi.getStatus(),
    staleTime: 10_000,
    retry: 1,
  })

  const targetPath = search.redirect || '/'

  // 如果已拥有登录态，直接跳转至重定向路径或首页
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

  // 纯粹由后端状态驱动：未初始化时展示向导，已初始化时展示登录
  const isSetupMode = Boolean(status && !status.initialized)

  function renderCard(): ReactElement {
    if (isStatusLoading) {
      /* 骨架屏：首屏探测未决时展示，杜绝布局抖动 (CLS = 0) */
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
              <Skeleton className='h-9 w-full' />
            </div>
            <div className='space-y-2'>
              <Skeleton className='h-3.5 w-12' />
              <Skeleton className='h-9 w-full' />
            </div>
            <div className='flex justify-between py-1'>
              <Skeleton className='h-4 w-24' />
              <Skeleton className='h-3.5 w-20' />
            </div>
            <Skeleton className='h-9 w-full rounded-md' />
          </CardContent>
        </Card>
      )
    }

    if (isSetupMode) {
      /* 首次启动初始化向导卡片（仅当后端明确返回 initialized: false 时展示） */
      return (
        <Card className='border-emerald-500/30 bg-card/90 shadow-2xl backdrop-blur-xl'>
          <CardHeader className='space-y-1.5 pb-5'>
            <div className='flex items-center gap-2.5'>
              <div className='flex size-9 items-center justify-center rounded-lg bg-emerald-500/10 text-emerald-600 ring-1 ring-emerald-500/20 dark:text-emerald-400'>
                <Shield className='size-4.5' />
              </div>
              <div>
                <CardTitle className='text-lg font-bold tracking-tight'>
                  系统初始化
                </CardTitle>
                <CardDescription className='text-xs'>
                  首次启动 · 请创建最高权限系统管理员
                </CardDescription>
              </div>
            </div>
          </CardHeader>
          <CardContent>
            <InitForm onSuccess={handleSuccess} />
          </CardContent>
        </Card>
      )
    }

    /* 常规管理员密码登录卡片 */
    return (
      <Card className='border-border/70 bg-card/90 shadow-2xl backdrop-blur-xl'>
        <CardHeader className='space-y-1.5 pb-5'>
          <div className='flex items-center gap-2.5'>
            <div className='flex size-9 items-center justify-center rounded-lg bg-primary/10 text-primary ring-1 ring-primary/20'>
              <Lock className='size-4.5' />
            </div>
            <div>
              <CardTitle className='text-lg font-bold tracking-tight'>
                管理员登录
              </CardTitle>
              <CardDescription className='text-xs'>
                请输入凭据以访问控制台
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

  return <AuthLayout>{renderCard()}</AuthLayout>
}
