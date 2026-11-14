import { useTranslation } from 'react-i18next'
import { ErrorLayout } from './error-layout'

export function NotFoundError() {
  const { t } = useTranslation()
  return (
    <ErrorLayout
      statusCode='404'
      title={t('errors.404.title')}
      description={t('errors.404.description')}
    />
  )
}
