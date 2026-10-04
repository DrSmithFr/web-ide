// IDE settings. They live in the pod (~/.web-ide/settings.json) as a history of full snapshots;
// the page keeps the current one in a store and saves a new snapshot after each change.
import { createStore, reconcile, unwrap } from 'solid-js/store'
import { createEffect, createRoot, createSignal } from 'solid-js'
import { on as onPod, request } from '../pod/rpc'
import { accentOf, monoOf, themeById, tokenTypes, toneVars, unfocusedParts } from '../settings/themes'
import { setGrammarOverrides } from '../editor/languages'
import type { GrammarDef } from '../editor/tokenizer'
import { setLang, type LangSetting } from '../i18n'

export interface Settings {
  theme: string
  /** Accent color id (settings/themes.ts), empty for the one of the theme. */
  accent: string
  /** Everything but the focused part of the window in grayscale. */
  visualFocus: boolean
  /** Accent outline around the focused part, and its width in pixels (1 to 3). */
  focusOutline: boolean
  focusOutlineWidth: number
  /** The parts out of focus dimmed, by focusDimLevel percent. */
  focusDim: boolean
  focusDimLevel: number
  tokenColors: Record<string, Record<string, string>>
  font: { family: string; size: number; lineHeight: number; ligatures: boolean; uiSize: number }
  editor: { tabSize: number; insertSpaces: boolean; highlightLine: boolean; indentGuides: boolean; showWhitespace: boolean; clipboardSize: number }
  keyboard: { layout: 'auto' | 'qwerty' | 'azerty'; overrides: Record<string, string[]> }
  syntax: Record<string, GrammarDef>
  language: LangSetting
}

export const defaultSettings: Settings = {
  theme: 'nuit',
  accent: '',
  visualFocus: false,
  focusOutline: false,
  focusOutlineWidth: 1,
  focusDim: false,
  focusDimLevel: 35,
  tokenColors: {},
  font: { family: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', ui-monospace, monospace", size: 13, lineHeight: 1.55, ligatures: true, uiSize: 13 },
  editor: { tabSize: 4, insertSpaces: true, highlightLine: true, indentGuides: true, showWhitespace: false, clipboardSize: 50 },
  keyboard: { layout: 'auto', overrides: {} },
  syntax: {},
  language: 'auto',
}

function merge(raw: any): Settings {
  const d = structuredClone(defaultSettings)
  if (!raw || typeof raw !== 'object') return d
  return {
    theme: raw.theme ?? d.theme,
    accent: typeof raw.accent === 'string' ? raw.accent : d.accent,
    visualFocus: raw.visualFocus === true,
    focusOutline: raw.focusOutline === true,
    focusOutlineWidth: [1, 2, 3].includes(raw.focusOutlineWidth) ? raw.focusOutlineWidth : d.focusOutlineWidth,
    focusDim: raw.focusDim === true,
    focusDimLevel: typeof raw.focusDimLevel === 'number' ? Math.min(70, Math.max(10, raw.focusDimLevel)) : d.focusDimLevel,
    tokenColors: raw.tokenColors ?? {},
    font: { ...d.font, ...(raw.font ?? {}) },
    editor: { ...d.editor, ...(raw.editor ?? {}) },
    keyboard: { ...d.keyboard, ...(raw.keyboard ?? {}), overrides: raw.keyboard?.overrides ?? {} },
    syntax: raw.syntax ?? {},
    language: raw.language === 'en' || raw.language === 'fr' ? raw.language : 'auto',
  }
}

const [settings, setSettingsStore] = createStore<Settings>(structuredClone(defaultSettings))
const [loaded, setLoaded] = createSignal(false)
export { settings, loaded as settingsLoaded }

let saveTimer: number | undefined
let pendingLabel = ''

/** Applies a change and records a new snapshot in the pod a moment later. */
export function updateSettings(fn: (s: Settings) => void, label = '') {
  const next = structuredClone(unwrap(settings)) as Settings
  fn(next)
  setSettingsStore(reconcile(next))
  if (label) pendingLabel = label
  clearTimeout(saveTimer)
  saveTimer = window.setTimeout(() => {
    const l = pendingLabel
    pendingLabel = ''
    request('settings.save', { settings: unwrap(settings), label: l }).catch((e) => console.error(e))
  }, 600)
}

/** Replaces all settings (import, rollback). */
export function replaceSettings(raw: any) {
  setSettingsStore(reconcile(merge(raw)))
}

export async function loadSettings() {
  try {
    const e = await request('settings.get')
    replaceSettings(e.settings)
  } finally {
    setLoaded(true)
  }
}

onPod('settings.changed', (e) => replaceSettings(e.settings))

// Applies theme, fonts and token colors to the page.
createRoot(() => {
  const style = document.createElement('style')
  style.id = 'theme-tokens'
  document.head.appendChild(style)
  createEffect(() => {
    const t = themeById(settings.theme)
    const root = document.documentElement
    for (const [k, v] of Object.entries(t.ui)) root.style.setProperty(`--${k}`, v)
    const [accent, accentFg] = accentOf(t, settings.accent)
    root.style.setProperty('--accent', accent)
    root.style.setProperty('--accent-fg', accentFg)
    root.dataset.theme = t.dark ? 'dark' : 'light'
    root.style.colorScheme = t.dark ? 'dark' : 'light'
    root.style.setProperty('--font-code', settings.font.family)
    root.style.setProperty('--font-size-code', `${settings.font.size}px`)
    root.style.setProperty('--line-height-code', String(settings.font.lineHeight))
    root.style.setProperty('--font-size-ui', `${settings.font.uiSize}px`)
    root.style.setProperty('--ligatures', settings.font.ligatures ? 'normal' : 'none')
    root.style.setProperty('--focus-outline-width', `${settings.focusOutlineWidth}px`)
    root.style.setProperty('--focus-dim', String(1 - settings.focusDimLevel / 100))
    const overrides = settings.tokenColors[t.id] ?? {}
    // Visual focus mode: tones of the theme instead of its colors out of focus.
    const tones: Record<string, string> = { ...t.ui, accent, 'accent-fg': accentFg }
    let css = `${unfocusedParts} { ${toneVars.map((v) => `--${v}: ${monoOf(t, tones[v])};`).join(' ')} }\n`
    for (const tok of tokenTypes) {
      const color = overrides[tok] ?? t.tokens[tok]
      if (!color) continue
      const extra = tok === 'comment' || tok === 'emphasis' ? 'font-style: italic;' : tok === 'link' ? 'text-decoration: underline;' : ''
      // Separate rules: a browser without ::highlight would drop a shared one. The spans
      // (code blocks of the assistant) use the same colors as the editor.
      css += `::highlight(tok-${tok}) { color: ${color}; ${extra} }\n.tok-${tok} { color: ${color}; ${extra} }\n`
      css += `${unfocusedParts} ::highlight(tok-${tok}) { color: ${monoOf(t, color)}; }\n${unfocusedParts} .tok-${tok} { color: ${monoOf(t, color)}; }\n`
    }
    style.textContent = css
  })
  createEffect(() => setLang(settings.language))
  // JSON.stringify reads the whole store, so the effect tracks every nested rule.
  createEffect(() => setGrammarOverrides(JSON.parse(JSON.stringify(settings.syntax))))
})
