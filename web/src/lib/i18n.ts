import commonEn from '@/locales/en/common.json'
import commonZhHans from '@/locales/zh-Hans/common.json'
import commonZhHant from '@/locales/zh-Hant/common.json'
import { type Locale, enUS, zhCN, zhTW } from 'date-fns/locale'
import i18n from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'
import alertPushEn from '@/features/alert-push/locales/en.json'
import alertPushZhHans from '@/features/alert-push/locales/zh-Hans.json'
import alertPushZhHant from '@/features/alert-push/locales/zh-Hant.json'
import algorithmsEn from '@/features/algorithms/locales/en.json'
import algorithmsZhHans from '@/features/algorithms/locales/zh-Hans.json'
import algorithmsZhHant from '@/features/algorithms/locales/zh-Hant.json'
import apiTokensEn from '@/features/api-tokens/locales/en.json'
import apiTokensZhHans from '@/features/api-tokens/locales/zh-Hans.json'
import apiTokensZhHant from '@/features/api-tokens/locales/zh-Hant.json'
import auditEn from '@/features/audit/locales/en.json'
import auditZhHans from '@/features/audit/locales/zh-Hans.json'
import auditZhHant from '@/features/audit/locales/zh-Hant.json'
import authEn from '@/features/auth/locales/en.json'
import authZhHans from '@/features/auth/locales/zh-Hans.json'
import authZhHant from '@/features/auth/locales/zh-Hant.json'
import camerasEn from '@/features/cameras/locales/en.json'
import camerasZhHans from '@/features/cameras/locales/zh-Hans.json'
import camerasZhHant from '@/features/cameras/locales/zh-Hant.json'
import gb28181En from '@/features/gb28181/locales/en.json'
import gb28181ZhHans from '@/features/gb28181/locales/zh-Hans.json'
import gb28181ZhHant from '@/features/gb28181/locales/zh-Hant.json'
import imageSearchEn from '@/features/image-search/locales/en.json'
import imageSearchZhHans from '@/features/image-search/locales/zh-Hans.json'
import imageSearchZhHant from '@/features/image-search/locales/zh-Hant.json'
import liveEn from '@/features/live/locales/en.json'
import liveZhHans from '@/features/live/locales/zh-Hans.json'
import liveZhHant from '@/features/live/locales/zh-Hant.json'
import personsEn from '@/features/persons/locales/en.json'
import personsZhHans from '@/features/persons/locales/zh-Hans.json'
import personsZhHant from '@/features/persons/locales/zh-Hant.json'
import recordsEn from '@/features/records/locales/en.json'
import recordsZhHans from '@/features/records/locales/zh-Hans.json'
import recordsZhHant from '@/features/records/locales/zh-Hant.json'
import networkEn from '@/features/settings/network/locales/en.json'
import networkZhHans from '@/features/settings/network/locales/zh-Hans.json'
import networkZhHant from '@/features/settings/network/locales/zh-Hant.json'
import storageEn from '@/features/settings/storage/locales/en.json'
import storageZhHans from '@/features/settings/storage/locales/zh-Hans.json'
import storageZhHant from '@/features/settings/storage/locales/zh-Hant.json'
import systemEn from '@/features/settings/system/locales/en.json'
import systemZhHans from '@/features/settings/system/locales/zh-Hans.json'
import systemZhHant from '@/features/settings/system/locales/zh-Hant.json'
import timeEn from '@/features/settings/time/locales/en.json'
import timeZhHans from '@/features/settings/time/locales/zh-Hans.json'
import timeZhHant from '@/features/settings/time/locales/zh-Hant.json'
import spatialEn from '@/features/spatial/locales/en.json'
import spatialZhHans from '@/features/spatial/locales/zh-Hans.json'
import spatialZhHant from '@/features/spatial/locales/zh-Hant.json'
import tasksEn from '@/features/tasks/locales/en.json'
import tasksZhHans from '@/features/tasks/locales/zh-Hans.json'
import tasksZhHant from '@/features/tasks/locales/zh-Hant.json'

export const SUPPORTED_LANGUAGES = ['zh-Hans', 'zh-Hant', 'en'] as const
export type SupportedLanguage = (typeof SUPPORTED_LANGUAGES)[number]

export const resources = {
  'zh-Hans': {
    common: commonZhHans,
    auth: authZhHans,
    network: networkZhHans,
    time: timeZhHans,
    storage: storageZhHans,
    system: systemZhHans,
    live: liveZhHans,
    cameras: camerasZhHans,
    gb28181: gb28181ZhHans,
    audit: auditZhHans,
    persons: personsZhHans,
    records: recordsZhHans,
    algorithms: algorithmsZhHans,
    tasks: tasksZhHans,
    spatial: spatialZhHans,
    imageSearch: imageSearchZhHans,
    alertPush: alertPushZhHans,
    apiTokens: apiTokensZhHans,
  },
  'zh-Hant': {
    common: commonZhHant,
    auth: authZhHant,
    network: networkZhHant,
    time: timeZhHant,
    storage: storageZhHant,
    system: systemZhHant,
    live: liveZhHant,
    cameras: camerasZhHant,
    gb28181: gb28181ZhHant,
    audit: auditZhHant,
    persons: personsZhHant,
    records: recordsZhHant,
    algorithms: algorithmsZhHant,
    tasks: tasksZhHant,
    spatial: spatialZhHant,
    imageSearch: imageSearchZhHant,
    alertPush: alertPushZhHant,
    apiTokens: apiTokensZhHant,
  },
  en: {
    common: commonEn,
    auth: authEn,
    network: networkEn,
    time: timeEn,
    storage: storageEn,
    system: systemEn,
    live: liveEn,
    cameras: camerasEn,
    gb28181: gb28181En,
    audit: auditEn,
    persons: personsEn,
    records: recordsEn,
    algorithms: algorithmsEn,
    tasks: tasksEn,
    spatial: spatialEn,
    imageSearch: imageSearchEn,
    alertPush: alertPushEn,
    apiTokens: apiTokensEn,
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

function syncDocumentLanguage(lng: string) {
  if (typeof document === 'undefined') return
  const htmlLang =
    lng === 'zh-Hant' ? 'zh-Hant' : lng === 'en' ? 'en' : 'zh-Hans'
  document.documentElement.lang = htmlLang
  const title = i18n.t('app.metaTitle', {
    defaultValue: '烛龙 · Zhulong | 边缘智能运维宿主',
  })
  if (title) {
    document.title = title
  }
}

syncDocumentLanguage(i18n.resolvedLanguage || i18n.language || 'zh-Hans')
i18n.on('languageChanged', syncDocumentLanguage)

export function getDateFnsLocale(
  lng = i18n.resolvedLanguage || i18n.language
): Locale {
  if (lng === 'zh-Hant') return zhTW
  if (lng === 'en') return enUS
  return zhCN
}

export function getIntlLocale(
  lng = i18n.resolvedLanguage || i18n.language
): string {
  if (lng === 'zh-Hant') return 'zh-Hant'
  if (lng === 'en') return 'en'
  return 'zh-Hans'
}

export default i18n
