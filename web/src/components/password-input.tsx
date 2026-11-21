import * as React from 'react'
import { Eye, EyeOff } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Button } from './ui/button'
import { Input } from './ui/input'

type PasswordInputProps = Omit<
  React.InputHTMLAttributes<HTMLInputElement>,
  'type'
> & {
  ref?: React.Ref<HTMLInputElement>
}

/**
 * 密码输入框 = shadcn `Input` + 显隐切换按钮。
 *
 * 复用 `Input` 而不是手写 `<input>`：`aria-invalid:border-destructive`
 * 这类状态样式只在 `Input` 里维护一份，手写副本会让「校验失败时密码框
 * 边框不变红」这种不一致再次发生。
 */
export function PasswordInput({
  className,
  disabled,
  ref,
  ...props
}: PasswordInputProps) {
  const [showPassword, setShowPassword] = React.useState(false)
  const { t } = useTranslation()

  return (
    <div className='relative'>
      <Input
        type={showPassword ? 'text' : 'password'}
        className={cn('pe-11', className)}
        ref={ref}
        disabled={disabled}
        {...props}
      />
      <Button
        type='button'
        size='icon'
        variant='ghost'
        disabled={disabled}
        // `before:` 把命中区从 36×36 扩到 44×44（WCAG 2.5.5 建议值），
        // 视觉尺寸不变，避免在 36px 输入框里塞一个过大的按钮。
        className='absolute inset-e-0.5 top-1/2 size-9 -translate-y-1/2 text-muted-foreground before:absolute before:-inset-1 before:content-[""]'
        onClick={() => setShowPassword((prev) => !prev)}
      >
        {showPassword ? <Eye size={18} /> : <EyeOff size={18} />}
        <span className='sr-only'>
          {showPassword
            ? t('components.passwordInput.hide')
            : t('components.passwordInput.show')}
        </span>
      </Button>
    </div>
  )
}
