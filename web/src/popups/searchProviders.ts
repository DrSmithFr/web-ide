// Search providers (docs/design/plugins.md, *Phase 2*): each one gives Search Everywhere one kind
// of results and its tab. The core has the files, the symbols and the actions; the plugins add
// theirs (the text of the project: Search) through the searchProviders contribution.
import { createMemo, createRoot, createSignal, type JSX } from 'solid-js'

export interface SearchHit {
  label: string
  detail?: string
  hint?: string
  icon?: () => JSX.Element
  /** Code labels (symbols, lines of text) and path details in monospace. */
  code?: boolean
  path?: boolean
  /** Ranges of the label to highlight (text matches). */
  ranges?: [number, number][]
  run: () => void
}

export interface SearchProvider {
  /** Id of its tab (files, symbols, actions, text…). */
  id: string
  /** Label of its tab and of its section in All. */
  label: string
  placeholder: string
  /** Place of its tab (the core's: 10, 20, 30). */
  order: number
  /** Results it gives to All (none: not in All). */
  inAll?: number
  /** Wait after the last key before searching (slow sources). */
  delay: number
  /** Results in its own tab (200 by default). */
  max?: number
  /** A shorter query is not searched (the empty list says so). */
  minChars?: number
  find(q: string, signal: AbortSignal, max: number): Promise<SearchHit[]>
}

const [core, setCore] = createSignal<SearchProvider[]>([])
const [fromPlugins, setFromPlugins] = createSignal<SearchProvider[]>([])

/** The providers of the core (set by Search Everywhere). */
export const setCoreProviders = setCore
/** The providers of the enabled plugins (set by the plugin registry). */
export const setPluginProviders = setFromPlugins

/** Every provider, in the order of their tabs. */
export const searchProviders = createRoot(() => createMemo(() => [...core(), ...fromPlugins()].sort((a, b) => a.order - b.order)))
