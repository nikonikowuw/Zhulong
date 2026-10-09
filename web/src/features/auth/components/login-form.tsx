import { type ReactElement, useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { ArrowRight, Loader2, User } from 'lucide-react'
import { toast } from 'sonner'
import { authApi, type AuthUser } from '../api/auth-api'
import { loginSchema, type LoginFormValues } from '../data/schema'
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
import { Checkbox } from '@/components/ui/checkbox'
import { Button } from '@/components/ui/button'
import { PasswordInput } from '@/components/password-input'

interface LoginFormProps {
  onSuccess?: (user: AuthUser) => void
}

export function LoginForm({ onSuccess }: LoginFormProps): ReactElement {
  const [isPending, setIsPending] = useState(false)
  const { auth } = useAuthStore()

  const form = useForm<LoginFormValues>({
    resolver: zodResolver(loginSchema),
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
      toast.success(`欢迎回来，管理员 ${user.username}！`)
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
            <FormItem className='space-y-2'>
              <FormLabel className='text-xs font-medium'>用户名</FormLabel>
              <FormControl>
                <div className='relative'>
                  <User className='absolute start-3 top-2.5 size-4 text-muted-foreground' />
                  <Input
                    {...field}
                    type='text'
                    placeholder='请输入用户名'
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
              <FormLabel className='text-xs font-medium'>密码</FormLabel>
              <FormControl>
                <PasswordInput
                  {...field}
                  placeholder='请输入密码'
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
              <FormItem className='flex flex-row items-center space-x-2 space-y-0'>
                <FormControl>
                  <Checkbox
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    disabled={isPending}
                  />
                </FormControl>
                <FormLabel className='cursor-pointer text-muted-foreground font-normal select-none hover:text-foreground'>
                  保持登录状态
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
              登录中...
            </>
          ) : (
            <>
              登录
              <ArrowRight className='ms-2 size-4' />
            </>
          )}
        </Button>
      </form>
    </Form>
  )
}
