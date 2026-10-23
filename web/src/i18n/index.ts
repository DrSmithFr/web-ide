// Translation of the interface. The English text is the key (and the English version);
// other languages map it to their text in a catalog. Parameters are written {name}.
import { createSignal } from 'solid-js'
import list from './languages.json'

/** A language of the interface: the one list (languages.json) every place reads. */
export type Language = { id: string; name: string; locale: string; singularBelow2?: boolean }
export type Lang = string
export type LangSetting = 'auto' | Lang

export const languages: Language[] = list

// English is the source text and has no catalog; every other language of the list has a
// `<id>.json` next to this file (the check script and `tsc` fail when one is missing).
const files = import.meta.glob<Record<string, string>>('./*.json', { eager: true, import: 'default' })
const catalogs: Record<Lang, Record<string, string>> = Object.fromEntries(
  languages.map((l) => [l.id, files[`./${l.id}.json`] ?? {}]),
)

// Each built-in plugin has its own catalogs (plugins/<id>/i18n/<lang>.json, shared with its
// messages in the pod), merged here at build: the core shows the labels of their manifests
// before their modules load.
const pluginFiles = import.meta.glob<Record<string, string>>('../../../plugins/*/i18n/*.json', { eager: true, import: 'default' })
const pluginCatalogs: Record<string, Record<Lang, Record<string, string>>> = {}
for (const [path, cat] of Object.entries(pluginFiles)) {
  const [, id, l] = path.match(/plugins\/([^/]+)\/i18n\/([^/]+)\.json$/)!
  ;(pluginCatalogs[id] ??= {})[l] = cat
}
const pluginsOf: Record<Lang, Record<string, string>> = Object.fromEntries(
  languages.map((l) => [l.id, Object.assign({}, ...Object.values(pluginCatalogs).map((c) => c[l.id] ?? {}))]),
)

/** Language of the browser when it is one of ours, else English. */
export function browserLang(): Lang {
  const l = (navigator.languages?.[0] ?? navigator.language ?? 'en').slice(0, 2).toLowerCase()
  return l in catalogs ? l : 'en'
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
  const tr = l === 'en' ? undefined : (catalogs[l][text] ?? pluginsOf[l][text])
  return fill(tr ?? text.slice(text.indexOf('|') + 1), params)
}

/** t() and tn() of a plugin: its own catalog first, then the core's (the shared texts). */
export function pluginT(id: string) {
  const own = pluginCatalogs[id] ?? {}
  const tp = (text: string, params?: Params): string => {
    const l = lang()
    const tr = l === 'en' ? undefined : own[l]?.[text]
    return tr !== undefined ? fill(tr, params) : t(text, params)
  }
  const tnp = (n: number, one: string, other: string, params?: Params): string => {
    const singular = current().singularBelow2 ? Math.abs(n) < 2 : Math.abs(n) === 1
    return tp(singular ? one : other, { n, ...params })
  }
  return { t: tp, tn: tnp }
}

const current = () => languages.find((l) => l.id === lang()) ?? languages[0]

/** Singular or plural form for n (n is also available as {n}). */
export function tn(n: number, one: string, other: string, params?: Params): string {
  const singular = current().singularBelow2 ? Math.abs(n) < 2 : Math.abs(n) === 1
  return t(singular ? one : other, { n, ...params })
}

/** Locale for dates and numbers. */
export function locale(): string {
  return current().locale
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

/** Relative time of a past date ("3 minutes ago"), in the language of the interface. */
export function fmtAgo(ms: number): string {
  const rtf = new Intl.RelativeTimeFormat(locale(), { numeric: 'auto' })
  const s = Math.round((ms - Date.now()) / 1000)
  const steps: [Intl.RelativeTimeFormatUnit, number][] = [['second', 60], ['minute', 60], ['hour', 24], ['day', 30], ['month', 12], ['year', Infinity]]
  let v = s
  for (const [unit, size] of steps) {
    if (Math.abs(v) < size) return rtf.format(v, unit)
    v = Math.round(v / size)
  }
  return fmtDate(ms)
}
