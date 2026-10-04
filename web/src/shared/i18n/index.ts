import i18n from "i18next";
import LanguageDetector from "i18next-browser-languagedetector";
import { initReactI18next } from "react-i18next";
import { authLocales } from "@/features/auth";
import en from "./locales/en.json";
import zhHans from "./locales/zh-Hans.json";
import zhHant from "./locales/zh-Hant.json";

export const supportedLanguages = ["en", "zh-Hans", "zh-Hant"] as const;
export type SupportedLanguage = (typeof supportedLanguages)[number];

function normalizeDetectedLanguage(language: string): SupportedLanguage {
  const normalizedLanguage = language.toLowerCase();
  if (normalizedLanguage.startsWith("zh")) {
    return /(hant|tw|hk|mo)/.test(normalizedLanguage) ? "zh-Hant" : "zh-Hans";
  }
  return "en";
}

const resources = {
  en: {
    translation: {
      ...en,
      auth: authLocales.en,
    },
  },
  "zh-Hans": {
    translation: {
      ...zhHans,
      auth: authLocales["zh-Hans"],
    },
  },
  "zh-Hant": {
    translation: {
      ...zhHant,
      auth: authLocales["zh-Hant"],
    },
  },
};

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    fallbackLng: "en",
    showSupportNotice: false,
    supportedLngs: [...supportedLanguages],
    detection: {
      order: ["localStorage", "navigator"],
      lookupLocalStorage: "zhulong.language.v1",
      caches: ["localStorage"],
      convertDetectedLanguage: normalizeDetectedLanguage,
    },
    interpolation: { escapeValue: false },
    returnNull: false,
  });

export function currentLanguage(language: string | undefined): SupportedLanguage {
  if (language?.startsWith("zh-Hant")) return "zh-Hant";
  if (language?.startsWith("zh-Hans")) return "zh-Hans";
  return "en";
}

export default i18n;
