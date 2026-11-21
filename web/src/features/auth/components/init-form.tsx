import { type ReactElement, useEffect, useMemo, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { type TFunction } from 'i18next'
import {
  AlertCircle,
  ArrowRight,
  CheckCircle2,
  Loader2,
  Lock,
  User,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { useAuthStore } from '@/stores/auth-store'
import { handleFormError } from '@/lib/form-error'
import { errorMessageOf, handleServerError } from '@/lib/handle-server-error'
import {
  STATUS_LEVEL_CLASS,
  statusDotClass,
  statusTextClass,
} from '@/lib/status-palette'
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
import { createInitAdminSchema, type InitAdminFormValues } from '../data/schema'

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

function getStrengthLevel(
  score: number,
  t: TFunction
): {
  label: string
  textClass: string
  barClass: string
} {
  if (score <= 1) {
    return {
      label: t('init.strengthWeak', { ns: 'auth' }),
      textClass: statusTextClass('destructive'),
      barClass: statusDotClass('destructive'),
    }
  }
  if (score === 2) {
    return {
      label: t('init.strengthMedium', { ns: 'auth' }),
      textClass: statusTextClass('warning'),
      barClass: statusDotClass('warning'),
    }
  }
  return {
    label: t('init.strengthStrong', { ns: 'auth' }),
    textClass: statusTextClass('active'),
    barClass: statusDotClass('active'),
  }
}

export function InitForm({ onSuccess }: InitFormProps): ReactElement {
  const [isPending, setIsPending] = useState(false)
  /** 无字段可归属的失败（断网 / 5xx / 已被初始化）走常驻横幅，而不是闪一下就没的 toast */
  const [formError, setFormError] = useState<string | null>(null)
  const { t } = useTranslation('auth')
  const { auth } = useAuthStore()

  const formSchema = useMemo(() => createInitAdminSchema(t), [t])

  // 与登录表单一致：只在宽视口自动聚焦，手机端不主动弹起软键盘。
  const [shouldAutoFocus] = useState(
    () => typeof window !== 'undefined' && window.innerWidth >= 640
  )

  const form = useForm<InitAdminFormValues>({
    resolver: zodResolver(formSchema),
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
  const strength = getStrengthLevel(passwordStrength, t)
  const isPasswordsMatch = Boolean(
    confirmPasswordValue &&
    passwordValue &&
    passwordValue === confirmPasswordValue
  )

  // 用户一改输入就撤回上一次的服务端错误
  useEffect(() => {
    const subscription = form.watch(() => setFormError(null))
    return () => subscription.unsubscribe()
  }, [form])

  async function onSubmit(values: InitAdminFormValues): Promise<void> {
    try {
      setIsPending(true)
      setFormError(null)
      const user = await authApi.initAdmin(values)
      auth.setUser(user)
      toast.success(t('init.success'))
      onSuccess?.(user)
    } catch (error) {
      const res = handleFormError(form, error)
      if (!res.hasFieldErrors) {
        setFormError(errorMessageOf(error))
        handleServerError(error, { silent: true })
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
            <FormItem className='space-y-1.5'>
              <FormLabel className='text-xs font-medium'>
                {t('init.usernameLabel')}
              </FormLabel>
              {/* relative 必须在 FormControl 外层，否则 label 的 htmlFor 会指向这层 div */}
              <div className='relative'>
                <User className='pointer-events-none absolute start-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground' />
                <FormControl>
                  <Input
                    {...field}
                    type='text'
                    placeholder={t('init.usernamePlaceholder')}
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
            <FormItem className='space-y-1.5'>
              <div className='flex items-center justify-between'>
                <FormLabel className='text-xs font-medium'>
                  {t('init.passwordLabel')}
                </FormLabel>
                {/* 空密码时显示「强度：弱」既没信息量又吓人；aria-live 让强度变化能被读屏播报 */}
                {passwordValue ? (
                  <span
                    className='text-xs text-muted-foreground'
                    aria-live='polite'
                  >
                    {t('init.strengthLabel')}
                    <span className={strength.textClass}>{strength.label}</span>
                  </span>
                ) : null}
              </div>
              <FormControl>
                <PasswordInput
                  {...field}
                  placeholder={t('init.passwordPlaceholder')}
                  className='h-11 text-sm'
                  autoComplete='new-password'
                  disabled={isPending}
                />
              </FormControl>
              {/* 强度条是纯装饰：语义已由上面那行文字承担，对读屏隐藏避免噪音。
                  没有输入时不占位，避免和「确认密码」一栏的间距不一致。 */}
              {passwordValue ? (
                <div
                  className='mt-1.5 flex h-1 w-full gap-1'
                  aria-hidden='true'
                >
                  {[1, 2, 3, 4].map((step) => (
                    <div
                      key={step}
                      className={cn(
                        'h-full flex-1 rounded-full motion-safe:transition-all motion-safe:duration-300',
                        passwordStrength >= step
                          ? strength.barClass
                          : 'bg-muted'
                      )}
                    />
                  ))}
                </div>
              ) : null}
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
                <FormLabel className='text-xs font-medium'>
                  {t('init.confirmPasswordLabel')}
                </FormLabel>
                {confirmPasswordValue && (
                  <span
                    className='flex items-center gap-1 text-xs'
                    aria-live='polite'
                  >
                    {isPasswordsMatch ? (
                      <span
                        className={cn(
                          'flex items-center',
                          statusTextClass('active')
                        )}
                      >
                        <CheckCircle2
                          className='me-0.5 size-3'
                          aria-hidden='true'
                        />
                        {t('init.passwordMatch')}
                      </span>
                    ) : (
                      <span className='text-destructive'>
                        {t('init.passwordMismatch')}
                      </span>
                    )}
                  </span>
                )}
              </div>
              <FormControl>
                <PasswordInput
                  {...field}
                  placeholder={t('init.confirmPasswordPlaceholder')}
                  className='h-11 text-sm'
                  autoComplete='new-password'
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
            <AlertCircle
              className='mt-px size-3.5 shrink-0'
              aria-hidden='true'
            />
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
              <Loader2
                className='me-2 size-4 animate-spin'
                aria-hidden='true'
              />
              {t('init.submitting')}
            </>
          ) : (
            <>
              {t('init.submit')}
              <ArrowRight className='ms-2 size-4' aria-hidden='true' />
            </>
          )}
        </Button>

        {/* 本地优先是个卖点，也是一条安全承诺：写清楚凭据去向，抵消「为什么要我在这儿设密码」 */}
        <p className='flex items-start justify-center gap-1.5 text-center text-xs text-muted-foreground'>
          <Lock className='mt-px size-3 shrink-0' aria-hidden='true' />
          <span>{t('init.trustNote')}</span>
        </p>
      </form>
    </Form>
  )
}
