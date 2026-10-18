// IDE settings. They live in the pod (~/.web-ide/settings.json) as a history of full snapshots;
// the page keeps the current one in a store and saves a new snapshot after each change.
import { createStore, reconcile, unwrap } from 'solid-js/store'
import { createEffect, createRoot, createSignal } from 'solid-js'
import { on as onPod, request } from '../pod/rpc'
import { accentOf, monoOf, themeById, tokenTypes, toneVars, unfocusedParts } from '../settings/themes'
import { setGrammarOverrides } from '../editor/languages'
import type { GrammarDef } from '../editor/tokenizer'
import { languages, setLang, type LangSetting } from '../i18n'
import { deviceId, FALLBACK, layoutFor, type DeviceLayout } from './device'

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
  /** wordWrap: soft wrap of long lines, for every file or only Markdown and text ones. */
  editor: { tabSize: number; insertSpaces: boolean; highlightLine: boolean; indentGuides: boolean; showWhitespace: boolean; wordWrap: 'off' | 'on' | 'markdown'; clipboardSize: number }
  keyboard: { layout: 'auto' | 'qwerty' | 'azerty'; overrides: Record<string, string[]> }
  syntax: Record<string, GrammarDef>
  language: LangSetting
  /** Layout per device (state/device.ts): the fonts and the panel sizes it chose. "fallback" holds
   * the layout saved for the devices that have none of their own. */
  devices: Record<string, DeviceLayout>
  /** Settings sync: the switches, read by the pod. The vault is opt-in. */
  sync: { settings: boolean; vault: boolean }
  /** Questions tool: a question of an agent opens in a modal by itself (a session may override it). */
  questions: { autoOpen: boolean }
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
  editor: { tabSize: 4, insertSpaces: true, highlightLine: true, indentGuides: true, showWhitespace: false, wordWrap: 'markdown', clipboardSize: 50 },
  keyboard: { layout: 'auto', overrides: {} },
  syntax: {},
  language: 'auto',
  devices: {},
  sync: { settings: true, vault: false },
  questions: { autoOpen: true },
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
    language: languages.some((l) => l.id === raw.language) ? raw.language : 'auto',
    devices: raw.devices ?? {},
    sync: { settings: raw.sync?.settings !== false, vault: raw.sync?.vault === true },
    questions: { autoOpen: raw.questions?.autoOpen !== false },
  }
}

const [settings, setSettingsStore] = createStore<Settings>(structuredClone(defaultSettings))
const [loaded, setLoaded] = createSignal(false)
export { settings, loaded as settingsLoaded }

/** The font in effect for a device: its own entry, else the layout saved for new devices. */
export function fontFor(device = deviceId): Settings['font'] {
  return { ...settings.font, ...(layoutFor(settings, device).font ?? {}) }
}

/** Records a value of this device (a font size, a panel size) as a normal settings change. The
 * entry of a device that never chose anything is created with its two parts, so the caller can set
 * one of them. */
export function updateDeviceLayout(fn: (d: DeviceLayout) => void, label = 'Layout') {
  updateSettings((s) => {
    const d = s.devices[deviceId] ?? (s.devices[deviceId] = {})
    if (!d.font) d.font = {}
    if (!d.panels) d.panels = {}
    fn(d)
  }, label)
}

/** Saves the layout of this device as the one new devices start from. */
export function saveFallbackLayout() {
  updateSettings((s) => {
    const own = layoutFor(s, deviceId)
    s.devices[FALLBACK] = { font: { ...own.font }, panels: { ...own.panels } }
  }, 'Fallback layout')
}

let saveTimer: number | undefined
let pendingLabel = ''
// The edit the user made, kept until the debounced save runs, as the leaves it changed (a path and
// its value, undefined for a removal). The edit is not kept as the function that made it: a handler
// reads the DOM event it was called with, which is gone when the timer runs. A change that arrives
// from another pod replaces the store while the save is pending; the edit must still reach the pod,
// on top of what the merge brought in.
let pendingEdit: Record<string, any> | null = null

const leafSep = '/'

const isObj = (v: any) => v !== null && typeof v === 'object' && !Array.isArray(v)

/** What an edit changed, path by path: the new value (a whole subtree when it appeared or moved),
 * undefined for a removal. `path` is the path of the node being compared ("" for the root). */
function editOf(before: any, after: any, path: string, out: Record<string, any>) {
  if (!isObj(before) || !isObj(after)) {
    if (JSON.stringify(before) !== JSON.stringify(after)) out[path] = after
    return
  }
  const child = (k: string) => (path ? path + leafSep + k : k)
  for (const [k, v] of Object.entries(after)) {
    if (!(k in before)) out[child(k)] = v
    else editOf(before[k], v, child(k), out)
  }
  for (const k of Object.keys(before)) if (!(k in after)) out[child(k)] = undefined
}

/** Applies the paths of an edit to a document read again after a merge. */
function applyEdit(target: any, edit: Record<string, any>) {
  for (const [path, value] of Object.entries(edit)) {
    const parts = path.split(leafSep)
    let node = target
    for (const p of parts.slice(0, -1)) {
      if (!isObj(node[p])) node[p] = {}
      node = node[p]
    }
    const last = parts[parts.length - 1]
    if (value === undefined) delete node[last]
    else node[last] = value
  }
}

/** Applies a change and records a new snapshot in the pod a moment later. */
export function updateSettings(fn: (s: Settings) => void, label = '') {
  const before = structuredClone(unwrap(settings)) as Settings
  const next = structuredClone(before) as Settings
  fn(next)
  setSettingsStore(reconcile(next))
  if (label) pendingLabel = label
  const edit: Record<string, any> = {}
  editOf(before, next, '', edit)
  pendingEdit = { ...(pendingEdit ?? {}), ...edit }
  clearTimeout(saveTimer)
  saveTimer = window.setTimeout(() => {
    const l = pendingLabel
    pendingLabel = ''
    const out = structuredClone(unwrap(settings)) as Settings
    if (pendingEdit) applyEdit(out, pendingEdit)
    pendingEdit = null
    request('settings.save', { settings: out, label: l }).catch((e) => console.error(e))
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
    // The sizes are per device: the family and the ligatures stay shared, the sizes a device
    // chose for itself do not move the other ones.
    const f = fontFor()
    root.style.setProperty('--font-code', settings.font.family)
    root.style.setProperty('--font-size-code', `${f.size}px`)
    root.style.setProperty('--line-height-code', String(f.lineHeight))
    root.style.setProperty('--font-size-ui', `${f.uiSize}px`)
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
