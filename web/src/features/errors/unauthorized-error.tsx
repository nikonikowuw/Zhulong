import { useTranslation } from 'react-i18next'
import { ErrorLayout } from './error-layout'

export function UnauthorizedError() {
  const { t } = useTranslation()
  return (
    <ErrorLayout
      statusCode='401'
      title={t('errors.401.title')}
      description={t('errors.401.description')}
    />
  )
}
