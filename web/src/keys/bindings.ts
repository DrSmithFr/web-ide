// Central key binding table. Bindings are stored by physical key (KeyboardEvent.code), so a
// preset per layout gives the expected letters. User changes are a list of overrides
// (action -> combos) on top of the preset; menus and the palette read the effective table.
import { createMemo, createRoot, createSignal } from 'solid-js'
import qwerty from './qwerty.json'
import azerty from './azerty.json'
import { settings } from '../state/settings'
import { t } from '../i18n'

export type Layout = 'qwerty' | 'azerty'
export const presets: Record<Layout, { name: string; labels: Record<string, string>; bindings: Record<string, string[]> }> = { qwerty, azerty }

export interface ActionDef {
  id: string
  label: string
  category: string
  /** Also runs while a terminal has the focus. */
  inTerminal?: boolean
  /** Shortcut outside the binding table, shown when the action has no combo (double Shift). */
  hint?: string
  /** Parameters of the label (dynamic actions: "Run: {name}"). */
  params?: Record<string, string>
  /** Icon and detail shown by Search Everywhere (dynamic actions). */
  icon?: string
  detail?: string
  /** Runs the action when no handler is registered (dynamic actions). */
  run?: () => void
  /** Default shortcuts outside the presets (actions of the plugins). */
  keys?: string[]
  /** Former id of the action: its shortcuts, set by the user or in the presets, still apply. */
  replaces?: string
}

export const actions: ActionDef[] = [
  { id: 'file.newFile', label: 'New file…', category: 'File' },
  { id: 'file.newFolder', label: 'New folder…', category: 'File' },
  { id: 'file.save', label: 'Save', category: 'File' },
  { id: 'file.saveAll', label: 'Save all', category: 'File' },
  { id: 'edit.undo', label: 'Undo', category: 'Editing' },
  { id: 'edit.redo', label: 'Redo', category: 'Editing' },
  { id: 'edit.duplicateLine', label: 'Duplicate the line', category: 'Editing' },
  { id: 'edit.deleteLine', label: 'Delete the line', category: 'Editing' },
  { id: 'edit.toggleComment', label: 'Comment / uncomment', category: 'Editing' },
  { id: 'edit.selectAll', label: 'Select all', category: 'Editing' },
  { id: 'edit.pasteHistory', label: 'Paste from history…', category: 'Editing' },
  { id: 'edit.nextOccurrence', label: 'Add the next occurrence to the selection', category: 'Editing' },
  { id: 'edit.unselectOccurrence', label: 'Remove the last occurrence from the selection', category: 'Editing' },
  { id: 'edit.allOccurrences', label: 'Select all the occurrences', category: 'Editing' },
  { id: 'edit.fold', label: 'Fold the block', category: 'Editing' },
  { id: 'edit.unfold', label: 'Unfold the block', category: 'Editing' },
  { id: 'edit.foldAll', label: 'Fold all the blocks', category: 'Editing' },
  { id: 'edit.unfoldAll', label: 'Unfold all the blocks', category: 'Editing' },
  { id: 'edit.complete', label: 'Complete the code', category: 'Editing' },
  { id: 'search.find', label: 'Find in the file', category: 'Search' },
  { id: 'search.global', label: 'Search in the project', category: 'Search' },
  { id: 'search.everywhere', label: 'Search everywhere', category: 'Search', inTerminal: true, hint: 'Double Shift' },
  { id: 'nav.gotoFile', label: 'Go to file…', category: 'Navigation', inTerminal: true },
  { id: 'nav.gotoSymbol', label: 'Go to symbol…', category: 'Navigation' },
  { id: 'nav.recentFiles', label: 'Recent files', category: 'Navigation' },
  { id: 'nav.switcher', label: 'Switcher', category: 'Navigation', inTerminal: true },
  { id: 'nav.fileStructure', label: 'File structure', category: 'Navigation' },
  { id: 'nav.gotoLine', label: 'Go to line…', category: 'Navigation' },
  { id: 'nav.back', label: 'Previous position', category: 'Navigation' },
  { id: 'nav.forward', label: 'Next position', category: 'Navigation' },
  { id: 'nav.previousConversation', label: 'Previous conversation', category: 'Navigation', inTerminal: true },
  { id: 'nav.nextConversation', label: 'Next conversation', category: 'Navigation', inTerminal: true },
  { id: 'nav.previousConsole', label: 'Previous console', category: 'Navigation', inTerminal: true },
  { id: 'nav.nextConsole', label: 'Next console', category: 'Navigation', inTerminal: true },
  { id: 'nav.subwordLeft', label: 'Previous sub-word', category: 'Navigation' },
  { id: 'nav.subwordRight', label: 'Next sub-word', category: 'Navigation' },
  { id: 'nav.subwordLeftSelect', label: 'Extend to the previous sub-word', category: 'Navigation' },
  { id: 'nav.subwordRightSelect', label: 'Extend to the next sub-word', category: 'Navigation' },
  { id: 'nav.related', label: 'Related symbols', category: 'Navigation' },
  { id: 'nav.test', label: 'Go to test / source', category: 'Navigation' },
  { id: 'lsp.definition', label: 'Go to declaration or usages', category: 'Code' },
  { id: 'lsp.implementation', label: 'Go to implementations', category: 'Code' },
  { id: 'lsp.typeDefinition', label: 'Go to type declaration', category: 'Code' },
  { id: 'lsp.superMethod', label: 'Go to super method', category: 'Code' },
  { id: 'lsp.references', label: 'Find usages', category: 'Code' },
  { id: 'lsp.hover', label: 'Quick documentation', category: 'Code' },
  { id: 'lsp.rename', label: 'Rename the symbol…', category: 'Code' },
  { id: 'lsp.format', label: 'Reformat the code', category: 'Code' },
  { id: 'conflict.resolve', label: 'Resolve the conflict', category: 'File' },
  { id: 'view.splitRight', label: 'Split right', category: 'View' },
  { id: 'view.splitDown', label: 'Split down', category: 'View' },
  { id: 'view.closeTab', label: 'Close the tab', category: 'View' },
  { id: 'view.closeOthers', label: 'Close the other tabs', category: 'View' },
  { id: 'view.closeAll', label: 'Close all the tabs', category: 'View' },
  { id: 'view.nextTab', label: 'Next tab', category: 'View' },
  { id: 'view.prevTab', label: 'Previous tab', category: 'View' },
  { id: 'view.toggleLeft', label: 'Show / hide the left panel', category: 'View', inTerminal: true },
  { id: 'view.toggleRight', label: 'Show / hide the tools', category: 'View', inTerminal: true },
  { id: 'view.toggleBottom', label: 'Show / hide the consoles', category: 'View', inTerminal: true },
  { id: 'view.resetTools', label: 'Reset the tool layout', category: 'View' },
  { id: 'view.visualFocus', label: 'Visual focus mode', category: 'View', inTerminal: true },
  { id: 'view.focusOutline', label: 'Focus outline', category: 'View', inTerminal: true },
  { id: 'view.focusDim', label: 'Dim out of focus', category: 'View', inTerminal: true },
  { id: 'view.whitespace', label: 'Show whitespace', category: 'View' },
  { id: 'view.wordWrap', label: 'Wrap the long lines', category: 'View' },
  { id: 'view.markdown', label: 'Markdown: rendered / raw', category: 'View' },
  { id: 'tool.explorer', label: 'Explorer', category: 'Tools', inTerminal: true },
  { id: 'tool.search', label: 'Search', category: 'Tools', inTerminal: true },
  { id: 'tool.git', label: 'Git', category: 'Tools', inTerminal: true },
  { id: 'tool.kanban', label: 'Kanban', category: 'Tools', inTerminal: true },
  { id: 'tool.assistant', label: 'Assistant', category: 'Tools', inTerminal: true },
  { id: 'tool.agents', label: 'Agents', category: 'Tools', inTerminal: true },
  { id: 'tool.questions', label: 'Questions', category: 'Tools', inTerminal: true },
  { id: 'assistant.doodle', label: 'New doodle', category: 'Tools', inTerminal: true },
  { id: 'assistant.fullComposer', label: 'Message box in full screen', category: 'Tools' },
  { id: 'tool.structure', label: 'Structure', category: 'Tools', inTerminal: true },
  { id: 'tool.problems', label: 'Problems', category: 'Tools', inTerminal: true },
  { id: 'tool.forwards', label: 'Port forwards and previews', category: 'Tools', inTerminal: true },
  { id: 'tool.info', label: 'Infos', category: 'Tools', inTerminal: true },
  { id: 'tool.console', label: 'Console', category: 'Tools', inTerminal: true },
  { id: 'tool.conflicts', label: 'Conflicts', category: 'Tools', inTerminal: true },
  { id: 'settings.open', label: 'Settings', category: 'General', inTerminal: true },
  { id: 'help.docs', label: 'Documentation', category: 'General' },
  { id: 'help.guide', label: 'User guide', category: 'General' },
  { id: 'help.glossary', label: 'Glossary', category: 'General' },
  { id: 'help.shortcuts', label: 'Keyboard shortcuts', category: 'General' },
  { id: 'kanban.open', label: 'Open the kanban', category: 'General' },
  { id: 'palette.open', label: 'Command palette', category: 'General', inTerminal: true },
  { id: 'menu.open', label: 'Open the menu bar', category: 'General', inTerminal: true },
  { id: 'console.new', label: 'New terminal', category: 'Consoles', inTerminal: true },
  { id: 'tile.launcher', label: 'Open in a new tile…', category: 'Desks', inTerminal: true },
  { id: 'tile.toggleSplit', label: 'New tiles: right / below', category: 'Desks', inTerminal: true },
  { id: 'tile.resize', label: 'Resize the tile (arrows, Esc)', category: 'Desks', inTerminal: true },
  { id: 'tile.growWidth', label: 'Wider tile', category: 'Desks', inTerminal: true },
  { id: 'tile.shrinkWidth', label: 'Narrower tile', category: 'Desks', inTerminal: true },
  { id: 'tile.growHeight', label: 'Taller tile', category: 'Desks', inTerminal: true },
  { id: 'tile.shrinkHeight', label: 'Shorter tile', category: 'Desks', inTerminal: true },
  { id: 'tile.column.left', label: 'Move the column left', category: 'Desks', inTerminal: true },
  { id: 'tile.column.right', label: 'Move the column right', category: 'Desks', inTerminal: true },
  { id: 'tile.column.width', label: 'Next width of the column', category: 'Desks', inTerminal: true },
  { id: 'tile.stack', label: 'Stack the tile in the column on the left', category: 'Desks', inTerminal: true },
  { id: 'tile.unstack', label: 'Take the tile out to a column of its own', category: 'Desks', inTerminal: true },
  ...(['left', 'right', 'up', 'down'] as const).flatMap((d) => [
    { id: `tile.focus.${d}`, label: `Focus the tile ${d === 'left' ? 'on the left' : d === 'right' ? 'on the right' : d === 'up' ? 'above' : 'below'}`, category: 'Desks', inTerminal: true },
    { id: `tile.swap.${d}`, label: `Swap with the tile ${d === 'left' ? 'on the left' : d === 'right' ? 'on the right' : d === 'up' ? 'above' : 'below'}`, category: 'Desks', inTerminal: true },
  ]),
  ...[1, 2, 3, 4, 5, 6, 7, 8, 9].flatMap((n) => [
    { id: `desk.go.${n}`, label: 'Go to desk {n}', params: { n: String(n) }, category: 'Desks', inTerminal: true },
    { id: `desk.send.${n}`, label: 'Send the tab to desk {n}', params: { n: String(n) }, category: 'Desks' },
  ]),
]

// Dynamic actions: built from data of the project (its saved commands), no default shortcut.
const [dynamicActions, setDynamicActions] = createSignal<ActionDef[]>([])
export { setDynamicActions }

// Actions of the enabled plugins: their tools and commands (plugins/registry.ts).
const [pluginActions, setPluginActions] = createSignal<ActionDef[]>([])
export { setPluginActions }

/** The static actions, then those of the plugins, then the dynamic ones. */
export const allActions = createRoot(() => createMemo(() => [...actions, ...pluginActions(), ...dynamicActions()]))
const actionIndex = createRoot(() => createMemo(() => new Map(allActions().map((a) => [a.id, a]))))

export function actionOf(id: string): ActionDef | undefined {
  return actionIndex().get(id)
}

/** Label of an action in the current language. */
export function actionLabel(a: ActionDef): string {
  return t(a.label, a.params)
}

// ---------- layout ----------

const [layoutMap, setLayoutMap] = createSignal<Map<string, string> | null>(null)
const [detected, setDetected] = createSignal<Layout | null>(null)
export { detected as detectedLayout }

/** Detection through navigator.keyboard (Chromium); elsewhere the language is only a hint. */
export async function detectLayout() {
  const kb = (navigator as any).keyboard
  if (kb?.getLayoutMap) {
    try {
      const m: Map<string, string> = await kb.getLayoutMap()
      setLayoutMap(new Map(m))
      setDetected(m.get('KeyQ') === 'a' ? 'azerty' : 'qwerty')
      return
    } catch {
      /* not allowed in this context */
    }
  }
  setDetected(null)
}

export function suggestedLayout(): Layout {
  return /^(fr|be)/i.test(navigator.language) ? 'azerty' : 'qwerty'
}

export const activeLayout = createRoot(() =>
  createMemo<Layout>(() => {
    const l = settings.keyboard.layout
    if (l !== 'auto') return l
    return detected() ?? suggestedLayout()
  }),
)

// ---------- effective table ----------

export const effectiveBindings = createRoot(() =>
  createMemo(() => {
    const base = presets[activeLayout()].bindings
    const out: Record<string, string[]> = {}
    const { overrides } = settings.keyboard
    for (const a of allActions()) {
      const old = a.replaces
      out[a.id] = overrides[a.id] ?? (old && overrides[old]) ?? base[a.id] ?? (old && base[old]) ?? a.keys ?? []
    }
    return out
  }),
)

const comboIndex = createRoot(() =>
  createMemo(() => {
    const idx = new Map<string, string[]>()
    for (const [action, combos] of Object.entries(effectiveBindings())) {
      for (const c of combos) {
        const list = idx.get(c) ?? []
        list.push(action)
        idx.set(c, list)
      }
    }
    return idx
  }),
)

export function comboFromEvent(e: KeyboardEvent): string | null {
  if (['ControlLeft', 'ControlRight', 'ShiftLeft', 'ShiftRight', 'AltLeft', 'AltRight', 'MetaLeft', 'MetaRight', 'AltGraph'].includes(e.code)) return null
  if (!e.code) return null
  // AltGr typing a character (Ctrl+Alt on Windows: @ # { [ | of an AZERTY) is text, not a shortcut.
  if (e.getModifierState?.('AltGraph') && e.key.length === 1) return null
  let s = ''
  if (e.ctrlKey) s += 'Ctrl+'
  if (e.altKey) s += 'Alt+'
  if (e.shiftKey) s += 'Shift+'
  if (e.metaKey) s += 'Meta+'
  return s + e.code
}

const keyNames: Record<string, string> = {
  ArrowLeft: '←', ArrowRight: '→', ArrowUp: '↑', ArrowDown: '↓', Backslash: '\\', Slash: '/', Period: '.', Comma: ',',
  Semicolon: ';', Quote: "'", Backquote: '`', BracketLeft: '[', BracketRight: ']', Minus: '-', Equal: '=', Enter: 'Enter',
  Escape: 'Esc', Space: 'Space', Backspace: '⌫', Delete: 'Del', PageUp: 'PgUp', PageDown: 'PgDn', Home: 'Home', End: 'End',
  Tab: 'Tab', IntlBackslash: '<',
}

export function keyLabel(code: string): string {
  // The detected layout names the keys, unless the user chose another layout than the detected one.
  const m = detected() === activeLayout() ? layoutMap() : null
  const fromLayout = m?.get(code)
  if (fromLayout && fromLayout.trim()) return fromLayout.length === 1 ? fromLayout.toUpperCase() : fromLayout
  const preset = presets[activeLayout()].labels[code]
  if (preset) return preset
  if (code.startsWith('Key')) return code.slice(3)
  if (code.startsWith('Digit')) return code.slice(5)
  if (code.startsWith('Numpad')) return t('Num {key}', { key: code.slice(6) })
  return keyNames[code] ? t(keyNames[code]) : code
}

export function comboLabel(combo: string): string {
  const parts = combo.split('+')
  const code = parts.pop()!
  return [...parts.map((m) => (m === 'Ctrl' ? 'Ctrl' : m === 'Alt' ? 'Alt' : m === 'Shift' ? t('Shift') : t('Meta'))), keyLabel(code)].join('+')
}

export function shortcutOf(action: string): string {
  const c = effectiveBindings()[action]?.[0]
  const hint = actionOf(action)?.hint
  return c ? comboLabel(c) : hint ? t(hint) : ''
}

/** Actions already bound to a combo (collision check before assigning). */
export function actionsFor(combo: string): string[] {
  return comboIndex().get(combo) ?? []
}

// ---------- dispatch ----------

export interface KeyContext {
  event: KeyboardEvent
  action: string
}

type ActionHandler = (ctx: KeyContext) => boolean | void

const handlers = new Map<string, ActionHandler[]>()

/** Registers a handler. The most recent one runs first; returning false passes to the next. */
export function registerAction(id: string, h: ActionHandler): () => void {
  const list = handlers.get(id) ?? []
  list.unshift(h)
  handlers.set(id, list)
  return () => {
    const l = handlers.get(id)
    if (l) handlers.set(id, l.filter((x) => x !== h))
  }
}

/** Runs an action from a menu or the palette. */
export function runAction(id: string, event?: KeyboardEvent): boolean {
  for (const h of handlers.get(id) ?? []) {
    if (h({ event: event ?? new KeyboardEvent('keydown'), action: id }) !== false) return true
  }
  const run = actionOf(id)?.run
  run?.()
  return !!run
}

let capturing: ((e: KeyboardEvent) => void) | null = null
/** Sends the next key presses to f instead of the bindings (shortcut editor). */
export function captureKeys(f: ((e: KeyboardEvent) => void) | null) {
  capturing = f
}

export function isCapturingKeys() {
  return !!capturing
}

export function installKeyHandler() {
  window.addEventListener(
    'keydown',
    (e) => {
      if (capturing) {
        e.preventDefault()
        e.stopPropagation()
        capturing(e)
        return
      }
      const combo = comboFromEvent(e)
      if (!combo) return
      const list = comboIndex().get(combo)
      if (!list) return
      const target = e.target as HTMLElement | null
      const inTerminal = !!target?.closest?.('.xterm')
      for (const id of list) {
        if (inTerminal && !actionOf(id)?.inTerminal) continue
        if (runAction(id, e)) {
          e.preventDefault()
          e.stopPropagation()
          return
        }
      }
    },
    true,
  )
}
