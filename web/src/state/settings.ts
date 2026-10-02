// IDE settings. They live in the pod (~/.web-ide/settings.json) as a history of full snapshots;
// the page keeps the current one in a store and saves a new snapshot after each change.
import { createStore, reconcile, unwrap } from 'solid-js/store'
import { createEffect, createRoot, createSignal } from 'solid-js'
import { on as onPod, request } from '../pod/rpc'
import { themeById, tokenTypes } from '../settings/themes'
import { setGrammarOverrides } from '../editor/languages'
import type { GrammarDef } from '../editor/tokenizer'

export interface Settings {
  theme: string
  tokenColors: Record<string, Record<string, string>>
  font: { family: string; size: number; lineHeight: number; ligatures: boolean; uiSize: number }
  editor: { tabSize: number; insertSpaces: boolean; highlightLine: boolean }
  keyboard: { layout: 'auto' | 'qwerty' | 'azerty'; overrides: Record<string, string[]> }
  syntax: Record<string, GrammarDef>
}

export const defaultSettings: Settings = {
  theme: 'nuit',
  tokenColors: {},
  font: { family: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', ui-monospace, monospace", size: 13, lineHeight: 1.55, ligatures: true, uiSize: 13 },
  editor: { tabSize: 4, insertSpaces: true, highlightLine: true },
  keyboard: { layout: 'auto', overrides: {} },
  syntax: {},
}

function merge(raw: any): Settings {
  const d = structuredClone(defaultSettings)
  if (!raw || typeof raw !== 'object') return d
  return {
    theme: raw.theme ?? d.theme,
    tokenColors: raw.tokenColors ?? {},
    font: { ...d.font, ...(raw.font ?? {}) },
    editor: { ...d.editor, ...(raw.editor ?? {}) },
    keyboard: { ...d.keyboard, ...(raw.keyboard ?? {}), overrides: raw.keyboard?.overrides ?? {} },
    syntax: raw.syntax ?? {},
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
    root.dataset.theme = t.dark ? 'dark' : 'light'
    root.style.colorScheme = t.dark ? 'dark' : 'light'
    root.style.setProperty('--font-code', settings.font.family)
    root.style.setProperty('--font-size-code', `${settings.font.size}px`)
    root.style.setProperty('--line-height-code', String(settings.font.lineHeight))
    root.style.setProperty('--font-size-ui', `${settings.font.uiSize}px`)
    root.style.setProperty('--ligatures', settings.font.ligatures ? 'normal' : 'none')
    const overrides = settings.tokenColors[t.id] ?? {}
    let css = ''
    for (const tok of tokenTypes) {
      const color = overrides[tok] ?? t.tokens[tok]
      if (!color) continue
      const extra = tok === 'comment' || tok === 'emphasis' ? 'font-style: italic;' : tok === 'link' ? 'text-decoration: underline;' : ''
      css += `::highlight(tok-${tok}) { color: ${color}; ${extra} }\n`
    }
    style.textContent = css
  })
  // JSON.stringify reads the whole store, so the effect tracks every nested rule.
  createEffect(() => setGrammarOverrides(JSON.parse(JSON.stringify(settings.syntax))))
})
