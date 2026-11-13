import { useEffect } from 'react'
import { useNavigate, useRouter } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { log } from '@/lib/logger'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'

type GeneralErrorProps = React.HTMLAttributes<HTMLDivElement> & {
  error?: unknown
  reset?: () => void
  minimal?: boolean
}

export function GeneralError({
  className,
  error,
  reset,
  minimal = false,
}: GeneralErrorProps) {
  const navigate = useNavigate()
  const { history } = useRouter()
  const { t } = useTranslation()

  useEffect(() => {
    if (error) {
      // 只上报稳定的事件类。真实异常消息与堆栈可能是任意长度、受外部输入
      // 影响，且常带凭据，因此只进开发期控制台（logger 内部处理），不上传。
      log('error', 'router.error_boundary', { boundary: 'route_error' })
    }
  }, [error])

  const errorMessage =
    error instanceof Error
      ? error.message
      : typeof error === 'string'
        ? error
        : null

  const handleReset = () => {
    if (reset) {
      reset()
    } else {
      window.location.reload()
    }
  }

  return (
    <div className={cn('h-svh w-full', className)}>
      <div className='m-auto flex h-full w-full flex-col items-center justify-center gap-2 px-4'>
        {!minimal && (
          <h1 className='text-[7rem] leading-tight font-bold'>500</h1>
        )}
        <span className='font-medium'>{t('errors.500.title')}</span>
        <p className='text-center text-muted-foreground'>
          {t('errors.500.description')}
        </p>

        {import.meta.env.DEV && errorMessage && (
          <div className='my-2 max-h-48 max-w-xl overflow-auto rounded-md border border-destructive/50 bg-destructive/10 p-3 text-left font-mono text-xs text-destructive'>
            <p className='mb-1 font-bold'>{t('errors.devTrace')}</p>
            <p className='break-all whitespace-pre-wrap'>{errorMessage}</p>
          </div>
        )}

        {!minimal && (
          <div className='mt-6 flex flex-wrap justify-center gap-4'>
            {reset && (
              <Button variant='default' onClick={handleReset}>
                {t('errors.tryAgain')}
              </Button>
            )}
            <Button variant='outline' onClick={() => history.go(-1)}>
              {t('errors.goBack')}
            </Button>
            <Button
              onClick={() => {
                reset?.()
                navigate({ to: '/' })
              }}
            >
              {t('errors.backToHome')}
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}
