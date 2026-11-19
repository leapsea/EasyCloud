import { computed, ref, watch } from 'vue'
import { createI18n } from 'vue-i18n'
import zhCN from './zh-CN'
import en from './en'

// Language preference is per browser. "auto" follows the browser language:
// any Chinese variant gets Simplified Chinese, everything else English.
export type LocalePref = 'auto' | 'zh-CN' | 'en'
export type Locale = 'zh-CN' | 'en'

const KEY = 'ec.locale'

export function browserLocale(): Locale {
  const langs = navigator.languages?.length ? navigator.languages : [navigator.language]
  return langs.some((l) => l?.toLowerCase().startsWith('zh')) ? 'zh-CN' : 'en'
}

function readPref(): LocalePref {
  try {
    const v = localStorage.getItem(KEY)
    return v === 'zh-CN' || v === 'en' ? v : 'auto'
  } catch {
    return 'auto'
  }
}

const pref = ref<LocalePref>(readPref())
const resolve = (p: LocalePref): Locale => (p === 'auto' ? browserLocale() : p)

export const i18n = createI18n({
  legacy: false,
  locale: resolve(pref.value),
  fallbackLocale: 'en',
  messages: { 'zh-CN': zhCN, en },
  missingWarn: false,
  fallbackWarn: false,
})

/** Translate outside components (stores, API client). */
export const t = i18n.global.t

export const locale = computed<Locale>(() => i18n.global.locale.value as Locale)

watch(pref, (p) => {
  i18n.global.locale.value = resolve(p)
  try { localStorage.setItem(KEY, p) } catch { /* private mode */ }
})
watch(locale, (l) => { document.documentElement.lang = l }, { immediate: true })
window.addEventListener('languagechange', () => { if (pref.value === 'auto') i18n.global.locale.value = browserLocale() })

export function useLocalePref() {
  return { pref, locale, setPref: (p: LocalePref) => (pref.value = p) }
}
