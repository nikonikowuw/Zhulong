import { type ReactElement, useEffect, useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { AlertCircle, ArrowRight, Loader2, User } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { useAuthStore } from '@/stores/auth-store'
import { handleFormError } from '@/lib/form-error'
import { errorMessageOf, handleServerError } from '@/lib/handle-server-error'
import { STATUS_LEVEL_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { PasswordInput } from '@/components/password-input'
import { authApi, type AuthUser } from '../api/auth-api'
import { createLoginSchema, type LoginFormValues } from '../data/schema'

interface LoginFormProps {
  onSuccess?: (user: AuthUser) => void
}

export function LoginForm({ onSuccess }: LoginFormProps): ReactElement {
  const [isPending, setIsPending] = useState(false)
  /** 无字段可归属的服务端失败同时显示常驻横幅与 toast：横幅保留上下文，toast 即时提醒。 */
  const [formError, setFormError] = useState<string | null>(null)
  const { t } = useTranslation('auth')
  const { auth } = useAuthStore()

  const formSchema = useMemo(() => createLoginSchema(t), [t])

  // 只在宽视口自动聚焦：桌面端少一次点击，手机端不主动弹起软键盘。
  // 不用 `(pointer: fine)` —— 部分无头/嵌套环境会把它报成 false，导致桌面端也不聚焦。
  const [shouldAutoFocus] = useState(
    () => typeof window !== 'undefined' && window.innerWidth >= 640
  )

  const form = useForm<LoginFormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: {
      username: '',
      password: '',
    },
  })

  // 用户一改输入就撤回上一次的服务端错误，避免「已经改了还挂着旧报错」
  useEffect(() => {
    const subscription = form.watch(() => setFormError(null))
    return () => subscription.unsubscribe()
  }, [form])

  async function onSubmit(values: LoginFormValues): Promise<void> {
    try {
      setIsPending(true)
      setFormError(null)
      const user = await authApi.login({
        username: values.username,
        password: values.password,
      })
      auth.setUser(user)
      toast.success(t('login.welcomeBack', { username: user.username }))
      onSuccess?.(user)
    } catch (error) {
      const res = handleFormError(form, error)
      if (!res.hasFieldErrors) {
        setFormError(errorMessageOf(error))
        handleServerError(error)
      }
    } finally {
      setIsPending(false)
    }
  }

  return (
    <Form {...form}>
      <form
        onSubmit={form.handleSubmit(onSubmit)}
        className='space-y-4'
        noValidate
      >
        <FormField
          control={form.control}
          name='username'
          render={({ field }) => (
            <FormItem className='space-y-2'>
              <FormLabel className='text-xs font-medium'>
                {t('login.usernameLabel')}
              </FormLabel>
              {/* relative 必须包在 FormControl 外层：FormControl 会把 id 与
                  aria-* clone 给「直接子元素」，多包一层 div 会让 label 的
                  htmlFor 指向 div，点击标签不再聚焦输入框。 */}
              <div className='relative'>
                <User className='pointer-events-none absolute start-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground' />
                <FormControl>
                  <Input
                    {...field}
                    type='text'
                    placeholder={t('login.usernamePlaceholder')}
                    className='h-11 ps-9 text-sm'
                    autoComplete='username'
                    autoFocus={shouldAutoFocus}
                    disabled={isPending}
                  />
                </FormControl>
              </div>
              <FormMessage className='text-xs' />
            </FormItem>
          )}
        />

        <FormField
          control={form.control}
          name='password'
          render={({ field }) => (
            <FormItem className='space-y-2'>
              <FormLabel className='text-xs font-medium'>
                {t('login.passwordLabel')}
              </FormLabel>
              <FormControl>
                <PasswordInput
                  {...field}
                  placeholder={t('login.passwordPlaceholder')}
                  className='h-11 text-sm'
                  autoComplete='current-password'
                  disabled={isPending}
                />
              </FormControl>
              <FormMessage className='text-xs' />
            </FormItem>
          )}
        />

        {formError ? (
          <div
            role='alert'
            className={cn(
              'flex items-start gap-2 rounded-md border px-3 py-2 text-xs',
              STATUS_LEVEL_CLASS.destructive
            )}
          >
            <AlertCircle className='mt-px size-3.5 shrink-0' />
            <span className='min-w-0 break-words'>{formError}</span>
          </div>
        ) : null}

        <Button
          type='submit'
          className='h-11 w-full font-medium shadow-sm transition-transform active:scale-[0.99]'
          disabled={isPending}
        >
          {isPending ? (
            <>
              <Loader2 className='me-2 size-4 animate-spin' />
              {t('login.submitting')}
            </>
          ) : (
            <>
              {t('login.submit')}
              <ArrowRight className='ms-2 size-4' />
            </>
          )}
        </Button>
      </form>
    </Form>
  )
}
