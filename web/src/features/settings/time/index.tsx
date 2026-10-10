import { useTranslation } from 'react-i18next'
import { ContentSection } from '../components/content-section'
import { TimeForm } from './components/time-form'

export function SettingsTime() {
  const { t } = useTranslation('time')

  return (
    <ContentSection
      title={t('page.title', { defaultValue: '系统对时' })}
      desc={t('page.desc', {
        defaultValue:
          '配置边缘宿主机时钟同步策略，支持 RFC 4330 SNTP 自动网络校时与现场一键同步浏览器时间。',
      })}
    >
      <TimeForm />
    </ContentSection>
  )
}
