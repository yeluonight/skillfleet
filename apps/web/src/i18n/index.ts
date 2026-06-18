// i18n/index.ts — react-i18next bootstrap (§13.8.6).
//
// Default language is zh-CN with en as the fallback. The chosen language is
// persisted in localStorage so a reload keeps the operator's choice; the
// Settings page (t12) flips it via i18n.changeLanguage, and every t() call
// re-renders live because react-i18next subscribes components to the change.
//
// Import this module once for its side effect (main.tsx does) before any
// component calls useTranslation.
import i18n from "i18next"
import { initReactI18next } from "react-i18next"

import zhCN from "./zh-CN"
import en from "./en"

// Dict is the shape of the default dictionary with every leaf widened to
// `string`. `as const` in zh-CN.ts narrows each value to a string *literal*,
// which is right for keying t() but wrong for en.ts (its English values would
// have to equal the Chinese literals). DeepStringify keeps the nested key
// structure — so a missing key still fails the build — while letting each
// translation supply its own string.
type DeepStringify<T> = {
  [K in keyof T]: T[K] extends string ? string : DeepStringify<T[K]>
}
export type Dict = DeepStringify<typeof zhCN>

export const SUPPORTED_LANGUAGES = ["zh-CN", "en"] as const
export type Language = (typeof SUPPORTED_LANGUAGES)[number]

const STORAGE_KEY = "skillfleet.lang"
export const DEFAULT_LANGUAGE: Language = "zh-CN"

// readStoredLanguage returns the persisted choice, or the default when unset
// or invalid. Guarded for SSR / privacy modes where localStorage may throw.
function readStoredLanguage(): Language {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    if (v && (SUPPORTED_LANGUAGES as readonly string[]).includes(v)) {
      return v as Language
    }
  } catch {
    // localStorage unavailable — fall through to the default.
  }
  return DEFAULT_LANGUAGE
}

// persistLanguage stores the operator's language choice. Best-effort: a
// storage failure must not break the in-memory language switch.
export function persistLanguage(lang: Language): void {
  try {
    localStorage.setItem(STORAGE_KEY, lang)
  } catch {
    // ignore — the switch still applies for this session.
  }
}

void i18n.use(initReactI18next).init({
  resources: {
    "zh-CN": { translation: zhCN },
    en: { translation: en },
  },
  lng: readStoredLanguage(),
  fallbackLng: DEFAULT_LANGUAGE,
  // Keys are dotted namespaces (auth.signIn). Values are plain strings, so
  // no plural/context separators are needed.
  interpolation: { escapeValue: false }, // React already escapes.
  returnNull: false,
})

export default i18n

// Strongly-type t(): the resources' `translation` namespace is our Dict, so
// t("auth.signIn") autocompletes and unknown keys are a type error.
declare module "i18next" {
  interface CustomTypeOptions {
    defaultNS: "translation"
    resources: { translation: Dict }
  }
}
