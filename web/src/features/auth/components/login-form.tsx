import { type ReactElement, useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { ArrowRight, Loader2, User } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { useAuthStore } from '@/stores/auth-store'
import { handleFormError } from '@/lib/form-error'
import { handleServerError } from '@/lib/handle-server-error'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
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
  const { t } = useTranslation('auth')
  const { auth } = useAuthStore()

  const formSchema = useMemo(() => createLoginSchema(t), [t])

  const form = useForm<LoginFormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: {
      username: '',
      password: '',
      rememberMe: true,
    },
  })

  async function onSubmit(values: LoginFormValues): Promise<void> {
    try {
      setIsPending(true)
      const user = await authApi.login({
        username: values.username,
        password: values.password,
      })
      auth.setUser(user)
      toast.success(t('login.welcomeBack', { username: user.username }))
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
            <FormItem className='space-y-2'>
              <FormLabel className='text-xs font-medium'>
                {t('login.usernameLabel')}
              </FormLabel>
              <FormControl>
                <div className='relative'>
                  <User className='absolute start-3 top-2.5 size-4 text-muted-foreground' />
                  <Input
                    {...field}
                    type='text'
                    placeholder={t('login.usernamePlaceholder')}
                    className='ps-9 text-sm'
                    autoComplete='username'
                    disabled={isPending}
                  />
                </div>
              </FormControl>
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
                  className='text-sm'
                  autoComplete='current-password'
                  disabled={isPending}
                />
              </FormControl>
              <FormMessage className='text-xs' />
            </FormItem>
          )}
        />

        <div className='flex items-center text-xs'>
          <FormField
            control={form.control}
            name='rememberMe'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center space-y-0 space-x-2'>
                <FormControl>
                  <Checkbox
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    disabled={isPending}
                  />
                </FormControl>
                <FormLabel className='cursor-pointer font-normal text-muted-foreground select-none hover:text-foreground'>
                  {t('login.rememberMe')}
                </FormLabel>
              </FormItem>
            )}
          />
        </div>

        <Button
          type='submit'
          className='w-full font-medium shadow-sm transition-transform active:scale-[0.99]'
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
