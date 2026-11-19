import { computed, reactive, watch } from 'vue'

// Appearance preferences are per browser (localStorage): they change how the
// app looks for this viewer only, and must render before the app has loaded.
export type ThemeMode = 'system' | 'light' | 'dark'

export const ACCENTS = [
  { key: 'blue', value: '#2563eb' },
  { key: 'indigo', value: '#4f46e5' },
  { key: 'violet', value: '#7c3aed' },
  { key: 'pink', value: '#db2777' },
  { key: 'red', value: '#dc2626' },
  { key: 'orange', value: '#ea580c' },
  { key: 'amber', value: '#d97706' },
  { key: 'emerald', value: '#059669' },
  { key: 'cyan', value: '#0891b2' },
  { key: 'slate', value: '#475569' },
] as const

export const DEFAULT_ACCENT = ACCENTS[0].value
const KEY = 'ec.theme'

function read(): { mode: ThemeMode; accent: string } {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? '{}')
    return {
      mode: ['system', 'light', 'dark'].includes(v.mode) ? v.mode : 'system',
      accent: /^#[0-9a-f]{6}$/i.test(v.accent) ? v.accent : DEFAULT_ACCENT,
    }
  } catch {
    return { mode: 'system', accent: DEFAULT_ACCENT }
  }
}

const media = window.matchMedia('(prefers-color-scheme: dark)')
const state = reactive({ ...read(), systemDark: media.matches })
media.addEventListener('change', (e) => (state.systemDark = e.matches))

/** Relative luminance (WCAG) of a #rrggbb colour. */
export function luminance(hex: string): number {
  const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4))
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2]
}

const dark = computed(() => (state.mode === 'system' ? state.systemDark : state.mode === 'dark'))

function apply() {
  const root = document.documentElement
  root.dataset.theme = dark.value ? 'dark' : 'light'
  root.style.setProperty('--brand', state.accent)
  // White text on dark accents, near-black on light ones (e.g. a pale yellow).
  root.style.setProperty('--on-brand', luminance(state.accent) > 0.45 ? '#111827' : '#ffffff')
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', dark.value ? '#0d121b' : '#f8fafc')
}

watch(() => [state.mode, state.accent, dark.value], () => {
  apply()
  try { localStorage.setItem(KEY, JSON.stringify({ mode: state.mode, accent: state.accent })) } catch { /* private mode */ }
}, { immediate: true })

export function useTheme() {
  return {
    state,
    dark,
    setMode: (m: ThemeMode) => (state.mode = m),
    setAccent: (hex: string) => { if (/^#[0-9a-f]{6}$/i.test(hex)) state.accent = hex.toLowerCase() },
    /** Cycles light → dark → system (for compact toggles). */
    cycle: () => (state.mode = state.mode === 'light' ? 'dark' : state.mode === 'dark' ? 'system' : 'light'),
  }
}

/** Mixes a #rrggbb colour with white (amount 0..1), e.g. to lift lines on dark backgrounds. */
export function tint(hex: string, amount: number): string {
  const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16))
  return '#' + c.map((v) => Math.round(v + (255 - v) * amount).toString(16).padStart(2, '0')).join('')
}
