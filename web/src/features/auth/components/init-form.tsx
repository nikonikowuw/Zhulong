import { type ReactElement, useMemo, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { type TFunction } from 'i18next'
import { ArrowRight, CheckCircle2, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { useAuthStore } from '@/stores/auth-store'
import { handleFormError } from '@/lib/form-error'
import { handleServerError } from '@/lib/handle-server-error'
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
      textClass: 'text-destructive',
      barClass: 'bg-destructive',
    }
  }
  if (score === 2) {
    return {
      label: t('init.strengthMedium', { ns: 'auth' }),
      textClass: 'text-amber-500',
      barClass: 'bg-amber-500',
    }
  }
  return {
    label: t('init.strengthStrong', { ns: 'auth' }),
    textClass: 'text-emerald-500',
    barClass: 'bg-emerald-500',
  }
}

export function InitForm({ onSuccess }: InitFormProps): ReactElement {
  const [isPending, setIsPending] = useState(false)
  const { t } = useTranslation('auth')
  const { auth } = useAuthStore()

  const formSchema = useMemo(() => createInitAdminSchema(t), [t])

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

  async function onSubmit(values: InitAdminFormValues): Promise<void> {
    try {
      setIsPending(true)
      const user = await authApi.initAdmin(values)
      auth.setUser(user)
      toast.success(t('init.success'))
      onSuccess?.(user)
    } catch (error) {
      const res = handleFormError(form, error, {
        onUnhandled: () => handleServerError(error),
      })
      if (!res.hasFieldErrors && !res.isAxiosError) {
        handleServerError(error)
      }
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
                {t('init.usernameLabel')}
              </FormLabel>
              <FormControl>
                <Input
                  {...field}
                  type='text'
                  placeholder={t('init.usernamePlaceholder')}
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
                  {t('init.passwordLabel')}
                </FormLabel>
                <span className='text-[11px] text-muted-foreground'>
                  {t('init.strengthLabel')}
                  <span className={strength.textClass}>{strength.label}</span>
                </span>
              </div>
              <FormControl>
                <PasswordInput
                  {...field}
                  placeholder={t('init.passwordPlaceholder')}
                  className='text-sm'
                  disabled={isPending}
                />
              </FormControl>
              {/* Password strength meter */}
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
                <FormLabel className='text-xs font-medium'>
                  {t('init.confirmPasswordLabel')}
                </FormLabel>
                {confirmPasswordValue && (
                  <span className='flex items-center gap-1 text-[11px]'>
                    {isPasswordsMatch ? (
                      <span className='flex items-center text-emerald-500'>
                        <CheckCircle2 className='me-0.5 size-3' />{' '}
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
              {t('init.submitting')}
            </>
          ) : (
            <>
              {t('init.submit')}
              <ArrowRight className='ms-2 size-4' />
            </>
          )}
        </Button>
      </form>
    </Form>
  )
}
