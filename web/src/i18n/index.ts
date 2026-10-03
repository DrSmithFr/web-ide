// Translation of the interface. The English text is the key (and the English version);
// other languages map it to their text in a catalog. Parameters are written {name}.
import { createSignal } from 'solid-js'
import fr from './fr.json'

export type Lang = 'en' | 'fr'
export type LangSetting = 'auto' | Lang

export const languages: { id: Lang; name: string }[] = [
  { id: 'en', name: 'English' },
  { id: 'fr', name: 'Français' },
]

const catalogs: Record<Lang, Record<string, string>> = { en: {}, fr }

/** Language of the browser when it is one of ours, else English. */
export function browserLang(): Lang {
  const l = (navigator.languages?.[0] ?? navigator.language ?? 'en').slice(0, 2).toLowerCase()
  return l in catalogs ? (l as Lang) : 'en'
}

const [lang, setLangSignal] = createSignal<Lang>(browserLang())
export { lang }

const listeners = new Set<(l: Lang) => void>()
/** Called with the new language (the pod is told so that its messages follow). */
export function onLangChange(f: (l: Lang) => void): () => void {
  listeners.add(f)
  return () => listeners.delete(f)
}

export function setLang(setting: LangSetting) {
  const l = setting === 'auto' ? browserLang() : setting
  document.documentElement.lang = l
  if (l === lang()) return
  setLangSignal(l)
  listeners.forEach((f) => f(l))
}

type Params = Record<string, string | number>

function fill(text: string, params?: Params) {
  return params ? text.replace(/\{(\w+)\}/g, (m, k) => (k in params ? String(params[k]) : m)) : text
}

/**
 * Text in the current language. Reactive: a view using t() follows a language change.
 * A key may carry a context to tell apart two meanings of one English text: 'menu|Edit'
 * shows "Edit" in English.
 */
export function t(text: string, params?: Params): string {
  const l = lang()
  const tr = l === 'en' ? undefined : catalogs[l][text]
  return fill(tr ?? text.slice(text.indexOf('|') + 1), params)
}

/** Singular or plural form for n (n is also available as {n}). */
export function tn(n: number, one: string, other: string, params?: Params): string {
  const singular = lang() === 'fr' ? Math.abs(n) < 2 : Math.abs(n) === 1
  return t(singular ? one : other, { n, ...params })
}

/** Locale for dates and numbers. */
export function locale(): string {
  return lang() === 'fr' ? 'fr-FR' : 'en-US'
}

export const fmtDate = (d: number | string | Date) => new Date(d).toLocaleString(locale())
export const fmtDay = (d: number | string | Date) => new Date(d).toLocaleDateString(locale())
export const fmtNumber = (n: number) => n.toLocaleString(locale())

/** Size in bytes with a unit of the language (o / Ko / Mo in French). */
export function fmtSize(n: number): string {
  if (n < 1024) return t('{n} B', { n })
  if (n < 1 << 20) return t('{n} KB', { n: (n / 1024).toFixed(0) })
  if (n < 1 << 30) return t('{n} MB', { n: (n / (1 << 20)).toFixed(1) })
  return t('{n} GB', { n: (n / (1 << 30)).toFixed(1) })
}
