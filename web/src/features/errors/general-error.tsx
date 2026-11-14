import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { log } from '@/lib/logger'
import { Button } from '@/components/ui/button'
import { ErrorLayout } from './error-layout'

type GeneralErrorProps = {
  className?: string
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
  const { t } = useTranslation()

  useEffect(() => {
    if (error) {
      // 只上报稳定的事件类。真实异常消息与堆栈可能是任意长度、受外部输入
      // 影响，且常带凭据，因此只进开发期控制台（logger 内部处理），不上传。
      log('error', 'router.error_boundary', { boundary: 'route_error' })
    }
  }, [error])

  const devErrorTrace =
    error instanceof Error
      ? (error.stack ?? error.message)
      : typeof error === 'string'
        ? error
        : null

  return (
    <ErrorLayout
      className={className}
      statusCode={minimal ? undefined : '500'}
      title={t('errors.500.title')}
      description={t('errors.500.description')}
      showDefaultActions={!minimal}
      onBeforeNavigate={reset}
      actions={
        !minimal && reset ? (
          <Button variant='default' onClick={reset}>
            {t('errors.tryAgain')}
          </Button>
        ) : null
      }
    >
      {import.meta.env.DEV && devErrorTrace ? (
        <div
          dir='ltr'
          className='my-2 max-h-48 max-w-xl overflow-auto rounded-md border border-destructive/50 bg-destructive/10 p-3 text-start font-mono text-xs text-destructive'
        >
          <p className='mb-1 font-bold'>{t('errors.devTrace')}</p>
          <p className='break-all whitespace-pre-wrap'>{devErrorTrace}</p>
        </div>
      ) : null}
    </ErrorLayout>
  )
}
