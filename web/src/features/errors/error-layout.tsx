import { type ReactNode } from 'react'
import { useNavigate, useRouter } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'

type ErrorLayoutProps = {
  className?: string
  statusCode?: string
  title: string
  description: string
  children?: ReactNode
  actions?: ReactNode
  showDefaultActions?: boolean
  onBeforeNavigate?: () => void
}

export function ErrorLayout({
  className,
  statusCode,
  title,
  description,
  children,
  actions,
  showDefaultActions = true,
  onBeforeNavigate,
}: ErrorLayoutProps) {
  const navigate = useNavigate()
  const { history } = useRouter()
  const { t } = useTranslation()

  const handleGoBack = () => {
    onBeforeNavigate?.()
    if (window.history.length > 1) {
      history.go(-1)
      return
    }
    void navigate({ to: '/' })
  }

  const handleBackToHome = () => {
    onBeforeNavigate?.()
    void navigate({ to: '/' })
  }

  return (
    <div
      className={cn(
        'flex h-svh w-full flex-col items-center justify-center gap-2 overflow-y-auto px-4 py-6',
        className
      )}
    >
      {statusCode ? (
        <span
          aria-hidden='true'
          className='text-[7rem] leading-tight font-bold'
        >
          {statusCode}
        </span>
      ) : null}
      <h1 className='text-center text-base font-medium'>{title}</h1>
      <p className='max-w-md text-center text-muted-foreground'>
        {description}
      </p>
      {children}
      {showDefaultActions || actions ? (
        <div className='mt-6 flex flex-wrap justify-center gap-4'>
          {actions}
          {showDefaultActions ? (
            <>
              <Button variant='outline' onClick={handleGoBack}>
                {t('errors.goBack')}
              </Button>
              <Button onClick={handleBackToHome}>
                {t('errors.backToHome')}
              </Button>
            </>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}
