import { useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { ErrorLayout } from './error-layout'

type MaintenanceErrorProps = {
  className?: string
  onRetry?: () => void
}

export function MaintenanceError({
  className,
  onRetry,
}: MaintenanceErrorProps) {
  const navigate = useNavigate()
  const { t } = useTranslation()

  const handleRetry = () => {
    if (onRetry) {
      onRetry()
      return
    }
    window.location.reload()
  }

  return (
    <ErrorLayout
      className={className}
      statusCode='503'
      title={t('errors.503.title')}
      description={t('errors.503.description')}
      showDefaultActions={false}
      actions={
        <>
          <Button variant='outline' onClick={handleRetry}>
            {t('errors.tryAgain')}
          </Button>
          <Button onClick={() => void navigate({ to: '/' })}>
            {t('errors.backToHome')}
          </Button>
        </>
      }
    />
  )
}
