import { useTranslation } from 'react-i18next'
import { ErrorLayout } from './error-layout'

export function ForbiddenError() {
  const { t } = useTranslation()
  return (
    <ErrorLayout
      statusCode='403'
      title={t('errors.403.title')}
      description={t('errors.403.description')}
    />
  )
}
