import { type ReactElement, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { ArrowRight, CheckCircle2, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { authApi, type AuthUser } from '../api/auth-api'
import { initAdminSchema, type InitAdminFormValues } from '../data/schema'
import { useAuthStore } from '@/stores/auth-store'
import { handleServerError } from '@/lib/handle-server-error'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { PasswordInput } from '@/components/password-input'

interface InitFormProps {
  onSuccess?: (user: AuthUser) => void
}

function getPasswordStrength(pwd: string): number {
  if (!pwd) return 0
  let score = 0
  if (pwd.length >= 8) score += 1
  if (pwd.length >= 12) score += 1
  if (/[A-Z]/.test(pwd) && /[a-z]/.test(pwd)) score += 1
  if (/[0-9]/.test(pwd) && /[^A-Za-z0-9]/.test(pwd)) score += 1
  return score
}

function getStrengthLevel(score: number): {
  label: string
  textClass: string
  barClass: string
} {
  if (score <= 1) {
    return { label: '弱', textClass: 'text-destructive', barClass: 'bg-destructive' }
  }
  if (score === 2) {
    return { label: '中', textClass: 'text-amber-500', barClass: 'bg-amber-500' }
  }
  return { label: '强', textClass: 'text-emerald-500', barClass: 'bg-emerald-500' }
}

export function InitForm({ onSuccess }: InitFormProps): ReactElement {
  const [isPending, setIsPending] = useState(false)
  const { auth } = useAuthStore()

  const form = useForm<InitAdminFormValues>({
    resolver: zodResolver(initAdminSchema),
    defaultValues: {
      username: '',
      password: '',
      confirmPassword: '',
    },
  })

  const passwordValue = useWatch({ control: form.control, name: 'password' })
  const confirmPasswordValue = useWatch({
    control: form.control,
    name: 'confirmPassword',
  })

  const passwordStrength = getPasswordStrength(passwordValue || '')
  const strength = getStrengthLevel(passwordStrength)
  const isPasswordsMatch = Boolean(
    confirmPasswordValue &&
      passwordValue &&
      passwordValue === confirmPasswordValue
  )

  async function onSubmit(values: InitAdminFormValues): Promise<void> {
    try {
      setIsPending(true)
      const user = await authApi.initAdmin(values)
      auth.setUser(user)
      toast.success('系统管理员账号初始化成功！')
      onSuccess?.(user)
    } catch (error) {
      handleServerError(error)
    } finally {
      setIsPending(false)
    }
  }

  return (
    <Form {...form}>
      <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-4'>
        <FormField
          control={form.control}
          name='username'
          render={({ field }) => (
            <FormItem className='space-y-1.5'>
              <FormLabel className='text-xs font-medium'>
                管理员用户名 (3~32 位)
              </FormLabel>
              <FormControl>
                <Input
                  {...field}
                  type='text'
                  placeholder='请输入用户名（例如：admin）'
                  className='text-sm'
                  disabled={isPending}
                />
              </FormControl>
              <FormMessage className='text-xs' />
            </FormItem>
          )}
        />

        <FormField
          control={form.control}
          name='password'
          render={({ field }) => (
            <FormItem className='space-y-1.5'>
              <div className='flex items-center justify-between'>
                <FormLabel className='text-xs font-medium'>
                  设置密码 (至少 8 位)
                </FormLabel>
                <span className='text-[11px] text-muted-foreground'>
                  强度：
                  <span className={strength.textClass}>{strength.label}</span>
                </span>
              </div>
              <FormControl>
                <PasswordInput
                  {...field}
                  placeholder='请输入密码'
                  className='text-sm'
                  disabled={isPending}
                />
              </FormControl>
              {/* 密码强度进度条 */}
              <div className='flex h-1 w-full gap-1 pt-1'>
                {[1, 2, 3, 4].map((step) => (
                  <div
                    key={step}
                    className={`h-full flex-1 rounded-full transition-all duration-300 ${
                      passwordStrength >= step ? strength.barClass : 'bg-muted'
                    }`}
                  />
                ))}
              </div>
              <FormMessage className='text-xs' />
            </FormItem>
          )}
        />

        <FormField
          control={form.control}
          name='confirmPassword'
          render={({ field }) => (
            <FormItem className='space-y-1.5'>
              <div className='flex items-center justify-between'>
                <FormLabel className='text-xs font-medium'>确认密码</FormLabel>
                {confirmPasswordValue && (
                  <span className='flex items-center gap-1 text-[11px]'>
                    {isPasswordsMatch ? (
                      <span className='flex items-center text-emerald-500'>
                        <CheckCircle2 className='size-3 me-0.5' /> 密码一致
                      </span>
                    ) : (
                      <span className='text-destructive'>两次密码不一致</span>
                    )}
                  </span>
                )}
              </div>
              <FormControl>
                <PasswordInput
                  {...field}
                  placeholder='请再次输入密码'
                  className='text-sm'
                  disabled={isPending}
                />
              </FormControl>
              <FormMessage className='text-xs' />
            </FormItem>
          )}
        />

        <Button
          type='submit'
          className='w-full font-medium shadow-sm transition-transform active:scale-[0.99]'
          disabled={isPending}
        >
          {isPending ? (
            <>
              <Loader2 className='me-2 size-4 animate-spin' />
              保存凭据中...
            </>
          ) : (
            <>
              激活并进入控制台
              <ArrowRight className='ms-2 size-4' />
            </>
          )}
        </Button>
      </form>
    </Form>
  )
}
