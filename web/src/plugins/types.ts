// Types of the plugin system (docs/design/plugins.md): the manifest sent by the pod, the state
// of a plugin on the project, and what a plugin's module gives the page.
import type { JSX } from 'solid-js'
import type { TabState } from '../state/project'
import type { Zone } from '../state/zones'
import type { PluginApi } from './api'
import type { SearchProvider } from '../popups/searchProviders'
import type { DiffSource } from '../ui/diffSources'

export interface PluginTool {
  id: string
  label: string
  icon: string
  zone: Zone
  /** The tool it comes before in its zone by default. */
  before?: string
}

export interface PluginManifest {
  id: string
  name: string
  version: string
  apiVersion: number
  permissions?: string[]
  provides?: string[]
  requires?: string[]
  namespaces?: string[]
  contributes: {
    tools?: PluginTool[]
    tabViews?: { id: string; label: string }[]
    assistantTools?: string[]
    /** Actions <plugin>.<id> (or the id as written when it has a prefix of the plugin's namespaces); keys: default shortcuts; replaces: the id of the action before it moved to a plugin. */
    commands?: { id: string; label: string; category?: string; keys?: string[]; replaces?: string; inTerminal?: boolean }[]
  }
}

/** A plugin as the project sees it (plugins.list): enabled, disabled by the user, or missing a capability. */
export interface PluginState {
  manifest: PluginManifest
  enabled: boolean
  disabled: boolean
  missing?: string[]
}

/** What the module of a plugin gives for the contributions of its manifest, by id. */
export interface PluginContributions {
  tools?: Record<string, { component: () => JSX.Element; badge?: () => number }>
  /** Tab views, opened as tabs of kind plugin:<plugin>/<view>. */
  views?: Record<string, (props: { tab: TabState; paneId: string }) => JSX.Element>
  commands?: Record<string, () => void>
  /** Sources of the diff view (the sides of a diff tab whose source is its id). */
  diffSources?: DiffSource[]
  /** Kinds of results of Search Everywhere, each with its tab. */
  searchProviders?: SearchProvider[]
  /** Components mounted once in the project window (a modal the plugin opens). */
  hosts?: (() => JSX.Element)[]
  /** Called when the window opens a project, and again after a reconnection (lists to fetch again). */
  opened?: () => void
  /** The plugin's options of a conversation of the assistant, given to its assistant tools. */
  assistantOptions?: () => unknown
}

/** The page module of a plugin: activated once, when the plugin is first enabled in the window. */
export interface PluginModule {
  activate(api: PluginApi): PluginContributions
  /** What it shows on the home page (no project open): the api offers its global methods there. */
  home?(api: PluginApi): () => JSX.Element
}
