import { useTranslation } from 'react-i18next'
import { ContentSection } from '../components/content-section'
import { AppearanceForm } from './appearance-form'

export function SettingsAppearance() {
  const { t } = useTranslation()

  return (
    <ContentSection
      title={t('appearance.title', { defaultValue: '外观与显示' })}
      desc={t('appearance.desc', {
        defaultValue:
          '定制边缘运维控制台的主题视觉模式与核心排版字体，适配日间明亮与暗光机房运维场景。',
      })}
    >
      <AppearanceForm />
    </ContentSection>
  )
}
