import commonEn from '@/locales/en/common.json'
import commonZhHans from '@/locales/zh-Hans/common.json'
import commonZhHant from '@/locales/zh-Hant/common.json'
import i18n from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'
import camerasEn from '@/features/cameras/locales/en.json'
import camerasZhHans from '@/features/cameras/locales/zh-Hans.json'
import camerasZhHant from '@/features/cameras/locales/zh-Hant.json'
import liveEn from '@/features/live/locales/en.json'
import liveZhHans from '@/features/live/locales/zh-Hans.json'
import liveZhHant from '@/features/live/locales/zh-Hant.json'
import networkEn from '@/features/settings/network/locales/en.json'
import networkZhHans from '@/features/settings/network/locales/zh-Hans.json'
import networkZhHant from '@/features/settings/network/locales/zh-Hant.json'
import timeEn from '@/features/settings/time/locales/en.json'
import timeZhHans from '@/features/settings/time/locales/zh-Hans.json'
import timeZhHant from '@/features/settings/time/locales/zh-Hant.json'

export const SUPPORTED_LANGUAGES = ['zh-Hans', 'zh-Hant', 'en'] as const
export type SupportedLanguage = (typeof SUPPORTED_LANGUAGES)[number]

export const resources = {
  'zh-Hans': {
    common: commonZhHans,
    network: networkZhHans,
    time: timeZhHans,
    live: liveZhHans,
    cameras: camerasZhHans,
  },
  'zh-Hant': {
    common: commonZhHant,
    network: networkZhHant,
    time: timeZhHant,
    live: liveZhHant,
    cameras: camerasZhHant,
  },
  en: {
    common: commonEn,
    network: networkEn,
    time: timeEn,
    live: liveEn,
    cameras: camerasEn,
  },
} as const

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    fallbackLng: 'zh-Hans',
    supportedLngs: SUPPORTED_LANGUAGES,
    defaultNS: 'common',
    fallbackNS: 'common',
    interpolation: {
      escapeValue: false,
    },
    detection: {
      order: ['localStorage', 'navigator'],
      lookupLocalStorage: 'i18nextLng',
      caches: ['localStorage'],
      convertDetectedLanguage: (lng) => {
        const lower = lng.toLowerCase()
        if (
          lower.startsWith('zh-tw') ||
          lower.startsWith('zh-hk') ||
          lower.startsWith('zh-mo') ||
          lower.startsWith('zh-hant')
        ) {
          return 'zh-Hant'
        }
        if (lower.startsWith('zh')) {
          return 'zh-Hans'
        }
        if (lower.startsWith('en')) {
          return 'en'
        }
        return 'zh-Hans'
      },
    },
  })

export default i18n
