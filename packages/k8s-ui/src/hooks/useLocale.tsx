import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'

export type Locale = 'zh-CN' | 'en'
export type Translate = (zh: string, en: string) => string

const storageKey = 'kubeatlas-portal-language'
const defaultLocale: Locale = 'zh-CN'

function initialLocale(): Locale {
  if (typeof window === 'undefined') return defaultLocale
  try {
    const stored = window.localStorage.getItem(storageKey)
    if (stored === 'zh-CN' || stored === 'en') return stored
  } catch { /* Browser preferences are optional. */ }

  const { languages, language } = window.navigator
  for (const tag of languages?.length ? languages : [language]) {
    const primary = tag.toLowerCase().split('-', 1)[0]
    if (primary === 'zh') return 'zh-CN'
    if (primary === 'en') return 'en'
  }
  return defaultLocale
}

type LocaleValue = { locale?: Locale; setLocale: (locale: Locale) => void; t: Translate }

const LocaleContext = createContext<LocaleValue | null>(null)
const chineseFallback: LocaleValue = {
  setLocale: () => {},
  t: zh => zh,
}
const englishFallback: LocaleValue = { ...chineseFallback, t: (_zh, en) => en }

export function LocaleProvider({ children }: { children: ReactNode }) {
  const [locale, updateLocale] = useState(initialLocale)
  const setLocale = useCallback((next: Locale) => {
    updateLocale(next)
    try {
      window.localStorage.setItem(storageKey, next)
    } catch { /* Browser preferences are optional. */ }
  }, [])

  useEffect(() => {
    const sync = (event: StorageEvent) => {
      if (event.key === storageKey || event.key === null) updateLocale(initialLocale())
    }
    window.addEventListener('storage', sync)
    return () => window.removeEventListener('storage', sync)
  }, [])

  useEffect(() => {
    document.documentElement.lang = locale
  }, [locale])

  const value = useMemo(() => ({
    locale,
    setLocale,
    t: (zh: string, en: string) => locale === 'en' ? en : zh,
  }), [locale, setLocale])

  return <LocaleContext.Provider value={value}>{children}</LocaleContext.Provider>
}

// Shared views keep their original copy and browser date formats without a provider.
export function useLocale(fallbackLanguage: Locale = defaultLocale): LocaleValue {
  return useContext(LocaleContext) ?? (fallbackLanguage === 'en' ? englishFallback : chineseFallback)
}
