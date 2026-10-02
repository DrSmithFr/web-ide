// Central key binding table. Bindings are stored by physical key (KeyboardEvent.code), so a
// preset per layout gives the expected letters. User changes are a list of overrides
// (action -> combos) on top of the preset; menus and the palette read the effective table.
import { createMemo, createRoot, createSignal } from 'solid-js'
import qwerty from './qwerty.json'
import azerty from './azerty.json'
import { settings } from '../state/settings'

export type Layout = 'qwerty' | 'azerty'
export const presets: Record<Layout, { name: string; labels: Record<string, string>; bindings: Record<string, string[]> }> = { qwerty, azerty }

export interface ActionDef {
  id: string
  label: string
  category: string
  /** Also runs while a terminal has the focus. */
  inTerminal?: boolean
}

export const actions: ActionDef[] = [
  { id: 'file.save', label: 'Enregistrer', category: 'Fichier' },
  { id: 'file.saveAll', label: 'Tout enregistrer', category: 'Fichier' },
  { id: 'edit.undo', label: 'Annuler', category: 'Édition' },
  { id: 'edit.redo', label: 'Rétablir', category: 'Édition' },
  { id: 'edit.duplicateLine', label: 'Dupliquer la ligne', category: 'Édition' },
  { id: 'edit.deleteLine', label: 'Supprimer la ligne', category: 'Édition' },
  { id: 'edit.toggleComment', label: 'Commenter / décommenter', category: 'Édition' },
  { id: 'edit.selectAll', label: 'Tout sélectionner', category: 'Édition' },
  { id: 'search.find', label: 'Rechercher dans le fichier', category: 'Recherche' },
  { id: 'search.global', label: 'Rechercher dans le projet', category: 'Recherche' },
  { id: 'nav.gotoFile', label: 'Aller au fichier…', category: 'Navigation', inTerminal: true },
  { id: 'nav.gotoSymbol', label: 'Aller au symbole…', category: 'Navigation' },
  { id: 'nav.fileStructure', label: 'Structure du fichier', category: 'Navigation' },
  { id: 'nav.gotoLine', label: 'Aller à la ligne…', category: 'Navigation' },
  { id: 'nav.back', label: 'Position précédente', category: 'Navigation' },
  { id: 'nav.forward', label: 'Position suivante', category: 'Navigation' },
  { id: 'nav.subwordLeft', label: 'Sous-mot précédent', category: 'Navigation' },
  { id: 'nav.subwordRight', label: 'Sous-mot suivant', category: 'Navigation' },
  { id: 'nav.subwordLeftSelect', label: 'Étendre au sous-mot précédent', category: 'Navigation' },
  { id: 'nav.subwordRightSelect', label: 'Étendre au sous-mot suivant', category: 'Navigation' },
  { id: 'nav.related', label: 'Symboles liés', category: 'Navigation' },
  { id: 'nav.test', label: 'Aller au test / à la source', category: 'Navigation' },
  { id: 'lsp.definition', label: 'Aller à la déclaration ou aux usages', category: 'Code' },
  { id: 'lsp.implementation', label: 'Aller aux implémentations', category: 'Code' },
  { id: 'lsp.typeDefinition', label: 'Aller à la déclaration de type', category: 'Code' },
  { id: 'lsp.superMethod', label: 'Aller à la super méthode', category: 'Code' },
  { id: 'lsp.references', label: 'Trouver les usages', category: 'Code' },
  { id: 'lsp.hover', label: 'Documentation rapide', category: 'Code' },
  { id: 'conflict.resolve', label: 'Résoudre le conflit', category: 'Fichier' },
  { id: 'view.splitRight', label: 'Diviser à droite', category: 'Affichage' },
  { id: 'view.splitDown', label: 'Diviser en bas', category: 'Affichage' },
  { id: 'view.closeTab', label: "Fermer l'onglet", category: 'Affichage' },
  { id: 'view.nextTab', label: 'Onglet suivant', category: 'Affichage' },
  { id: 'view.prevTab', label: 'Onglet précédent', category: 'Affichage' },
  { id: 'view.toggleLeft', label: 'Afficher / masquer le panneau gauche', category: 'Affichage', inTerminal: true },
  { id: 'view.toggleRight', label: 'Afficher / masquer les tools', category: 'Affichage', inTerminal: true },
  { id: 'view.toggleBottom', label: 'Afficher / masquer les consoles', category: 'Affichage', inTerminal: true },
  { id: 'settings.open', label: 'Réglages', category: 'Général', inTerminal: true },
  { id: 'palette.open', label: 'Palette de commandes', category: 'Général', inTerminal: true },
  { id: 'console.new', label: 'Nouveau terminal', category: 'Consoles', inTerminal: true },
  { id: 'sql.execute', label: 'Exécuter la requête active', category: 'Base de données' },
]

export const actionById = new Map(actions.map((a) => [a.id, a]))

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
    for (const a of actions) out[a.id] = settings.keyboard.overrides[a.id] ?? base[a.id] ?? []
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
  let s = ''
  if (e.ctrlKey) s += 'Ctrl+'
  if (e.altKey) s += 'Alt+'
  if (e.shiftKey) s += 'Shift+'
  if (e.metaKey) s += 'Meta+'
  return s + e.code
}

const keyNames: Record<string, string> = {
  ArrowLeft: '←', ArrowRight: '→', ArrowUp: '↑', ArrowDown: '↓', Backslash: '\\', Slash: '/', Period: '.', Comma: ',',
  Semicolon: ';', Quote: "'", Backquote: '`', BracketLeft: '[', BracketRight: ']', Minus: '-', Equal: '=', Enter: 'Entrée',
  Escape: 'Échap', Space: 'Espace', Backspace: '⌫', Delete: 'Suppr', PageUp: 'PgPréc', PageDown: 'PgSuiv', Home: 'Début', End: 'Fin',
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
  if (code.startsWith('Numpad')) return 'Pavé ' + code.slice(6)
  return keyNames[code] ?? code
}

export function comboLabel(combo: string): string {
  const parts = combo.split('+')
  const code = parts.pop()!
  return [...parts.map((m) => (m === 'Ctrl' ? 'Ctrl' : m === 'Alt' ? 'Alt' : m === 'Shift' ? 'Maj' : 'Méta')), keyLabel(code)].join('+')
}

export function shortcutOf(action: string): string {
  const c = effectiveBindings()[action]?.[0]
  return c ? comboLabel(c) : ''
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
  return false
}

let capturing: ((e: KeyboardEvent) => void) | null = null
/** Sends the next key presses to f instead of the bindings (shortcut editor). */
export function captureKeys(f: ((e: KeyboardEvent) => void) | null) {
  capturing = f
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
        if (inTerminal && !actionById.get(id)?.inTerminal) continue
        for (const h of handlers.get(id) ?? []) {
          if (h({ event: e, action: id }) !== false) {
            e.preventDefault()
            e.stopPropagation()
            return
          }
        }
      }
    },
    true,
  )
}
