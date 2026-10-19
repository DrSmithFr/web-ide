// Capabilities as services (docs/design/plugins.md): a plugin that provides a capability
// registers its implementation when it is activated (api.provide); the core and the other
// plugins get it with capability() / api.use(), undefined while the plugin providing it is off.
import { createMutable } from 'solid-js/store'
import type { JSX } from 'solid-js'
import type { Doc } from '../editor/doc'
import type { CompletionItem, CompletionList } from '../editor/completion'

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

export interface GitFile {
  path: string
  origPath?: string
  index: string
  work: string
  untracked?: boolean
  conflict?: boolean
}

export interface GitStatus {
  repo: boolean
  top?: string
  branch?: string
  head?: string
  upstream?: string
  ahead: number
  behind: number
  files: GitFile[]
}

export type FileGitState = 'modified' | 'added' | 'untracked' | 'conflict' | 'deleted'

export interface GitBranch {
  name: string
  current: boolean
  remote?: boolean
  upstream?: string
}

/** git (the Git plugin): the state of the repository of the worktree shown, and what reads it. */
export interface GitService {
  status: () => GitStatus | null
  /** Bumped when HEAD or the index may have changed (diffs and markers are recomputed). */
  revision: () => number
  refresh(delay?: number): void
  /** Git state of a file, of a folder from its content, whether a folder holds changes. */
  fileState(path: string): FileGitState | null
  dirState(dir: string): FileGitState | null
  dirChanged(dir: string): boolean
  ignored(paths: string[]): Promise<string[]>
  branches(): Promise<GitBranch[]>
  /** A file at a revision (HEAD, a commit; '' the index). */
  show(path: string, rev: string): Promise<{ content: string; exists: boolean }>
}

export interface PreviewSpec {
  project: string
  title: string
  command: string
  cwd?: string
  port: number
  /** Saved command of the project it starts (its console, one per command). */
  commandId?: string
}

export interface Preview extends PreviewSpec {
  id: string
  console: string
  url: string
  publicUrl?: string
  public: boolean
  tailscale: boolean
  started: number
}

/** previews (the Previews plugin): the app previews of the pod, shared by every window. */
export interface PreviewsService {
  list: () => Preview[]
  /** Starts (or finds) the preview of a spec and opens it in a new tab. */
  open(spec: PreviewSpec): Promise<Preview>
  close(id: string): Promise<unknown>
  setPublic(id: string, pub: boolean): Promise<Preview>
  url(p: Preview): string
  same(p: PreviewSpec, s: PreviewSpec): boolean
}

/** A diagnostic of a language server (LSP). */
export interface LspDiagnostic {
  range: { start: { line: number; character: number }; end: { line: number; character: number } }
  severity?: number
  message: string
  source?: string
  code?: string | number
}

/** language (the Languages plugin): what the editor asks the language servers. */
export interface LanguageService {
  /** The diagnostics of a file (reactive). */
  diagnostics(path: string): LspDiagnostic[]
  /** The completion of a server at a caret; null when the file has none (the editor offers its words). */
  complete(doc: Doc, offset: number, trigger: string | null, signal: AbortSignal): Promise<CompletionList | null>
  resolve(doc: Doc, item: CompletionItem): Promise<CompletionItem>
  triggerCharacters(doc: Doc): Promise<string[]>
  /** Ctrl+click: the declaration (or the usages) of the symbol at an offset. */
  gotoDeclaration(doc: Doc, offset: number): void
  /** Shows the Problems tool on its list. */
  showProblems(): void
}

export interface Capabilities {
  terminal: TerminalService
  commands: CommandsService
  git: GitService
  previews: PreviewsService
  language: LanguageService
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
