// Types of the plugin system (docs/design/plugins.md): the manifest sent by the pod, the state
// of a plugin on the project, and what a plugin's module gives the page.
import type { JSX } from 'solid-js'
import type { TabState } from '../state/project'
import type { Zone } from '../state/zones'
import type { MenuItem, PluginApi } from './api'
import type { UiTool } from '../ui/uiCalls'
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
  /** Locked: always enabled, the IDE does not run without it (Files, Code). */
  required?: boolean
  permissions?: string[]
  provides?: string[]
  requires?: string[]
  namespaces?: string[]
  contributes: {
    tools?: PluginTool[]
    tabViews?: { id: string; label: string }[]
    assistantTools?: string[]
    mcpTools?: string[]
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
  /** The view of the file tabs (one plugin gives it: Code). */
  fileView?: (props: { tab: TabState; paneId: string }) => JSX.Element
  /** Tab views, opened as tabs of kind plugin:<plugin>/<view>. */
  views?: Record<string, (props: { tab: TabState; paneId: string }) => JSX.Element>
  /** The title of a tab of one of these views, from its state (the label of the manifest otherwise). */
  viewTitle?: (tab: TabState) => string | undefined
  /** Views alone in their tile on a desk of tiles, like a tool (the Kanban board). */
  aloneViews?: string[]
  /** Items of the status bar while a file tab has the focus (the plugin giving fileView: Code). */
  fileStatus?: (tab: TabState) => StatusItem[]
  /** Items of the status bar while a tab of one of the plugin's views has the focus. */
  viewStatus?: (tab: TabState) => StatusItem[]
  /** Items of the status bar while a tool of the plugin has the focus, by tool id. */
  toolStatus?: Record<string, () => StatusItem[]>
  /** Tools of the interface the pod asks the window to run (ui.call), by name: the AI draws a page of its board. */
  uiTools?: Record<string, UiTool>
  commands?: Record<string, () => void>
  /** Items of the context menu of an entry of the Explorer (Exec: a terminal here). */
  explorerItems?: (entry: ExplorerEntry) => MenuItem[]
  /** Decorations of the Explorer: the files left out, the state of a file (Git: modified…). */
  fileDecorations?: FileDecorations
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

/**
 * An item of the status bar, on its right. Read in a memo: its fields may change with what it
 * shows. A click runs its action, or opens its menu (its options) above it.
 */
export interface StatusItem {
  id: string
  text: string
  icon?: string
  title?: string
  testid?: string
  action?: () => void
  menu?: () => MenuItem[]
}

/** The page module of a plugin: activated once, when the plugin is first enabled in the window. */
export interface PluginModule {
  activate(api: PluginApi): PluginContributions
  /** What it shows on the home page (no project open): the api offers its global methods there. */
  home?(api: PluginApi): () => JSX.Element
}

/** An entry of the Explorer, and the folder it is or is in. */
export interface ExplorerEntry {
  path: string
  dir: boolean
  folder: string
}

export interface FileDecorations {
  /** The paths among these that are left out (dimmed, as their content). */
  ignored?(paths: string[]): Promise<string[]>
  /** A state of a file or a folder (a class of its name), null for none. */
  state(path: string, dir: boolean): string | null
}
