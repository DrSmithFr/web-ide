// Capabilities as services (docs/design/plugins.md): a plugin that provides a capability
// registers its implementation when it is activated (api.provide); the core and the other
// plugins get it with capability() / api.use(), undefined while the plugin providing it is off.
import { createMutable } from 'solid-js/store'
import type { JSX } from 'solid-js'

/** A console of a project: a terminal or a task (one command). */
export interface ConsoleInfo {
  id: string
  title: string
  kind: 'terminal' | 'task'
  command?: string[]
  exited: boolean
  code: number
  /** Worktree the console runs in (set by the page). */
  project?: string
  /** Saved command the task runs. */
  commandId?: string
}

/** terminal (the Exec plugin): the consoles of the window. */
export interface TerminalService {
  consoles: () => ConsoleInfo[]
  /** Starts a console; it shows in the Console tool unless show is false (a tile takes it). */
  open(o?: { cwd?: string; command?: string[]; title?: string; kind?: 'terminal' | 'task'; show?: boolean }): Promise<ConsoleInfo | undefined>
  /** Shows a console in the Console tool. */
  show(id: string): void
  /** A console in a container element (a tile, a tab, a detached window). */
  TermView: (props: { id: string; focus?: boolean }) => JSX.Element
  /** The consoles of a worktree, as project.open lists them. */
  setList(list: ConsoleInfo[], project?: string): void
  /** The console tab shown before (-1) or after (1). */
  navigate(dir: -1 | 1): void
}

/** A saved command of the project. */
export interface SavedCommand {
  id: string
  name: string
  description?: string
  command: string
  flag: 'serve' | 'build' | 'test' | 'lint' | 'setup' | 'other'
  cwd?: string
  port?: number
}

/** commands (the Exec plugin): the saved commands of the project. */
export interface CommandsService {
  list: () => SavedCommand[]
  load(): Promise<void>
  /** The editor of the commands (a modal), on a command or a draft. */
  edit(o?: { select?: string; draft?: Partial<SavedCommand> }): void
}

export interface Capabilities {
  terminal: TerminalService
  commands: CommandsService
}

// The implementations, by capability, and the plugin providing each.
const services = createMutable<{ impl: Partial<Capabilities>; by: Partial<Record<keyof Capabilities, string>> }>({ impl: {}, by: {} })
let enabledNow: (plugin: string) => boolean = () => false

/** Set by the registry: whether a plugin is enabled on the project (reactive). */
export function setCapabilityGate(f: (plugin: string) => boolean) {
  enabledNow = f
}

export function provide<K extends keyof Capabilities>(plugin: string, name: K, impl: Capabilities[K]) {
  services.impl[name] = impl
  services.by[name] = plugin
}

/** A capability of the project, undefined while its provider is off (reactive). */
export function capability<K extends keyof Capabilities>(name: K): Capabilities[K] | undefined {
  const by = services.by[name]
  return by && enabledNow(by) ? (services.impl[name] as Capabilities[K]) : undefined
}
