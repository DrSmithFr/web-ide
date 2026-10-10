// Saved commands of the project (Console ▶ menu): kept by the pod in .ide/commands.json of
// the main project, run as tasks in the worktree shown, one console per command.
import { createEffect, createRoot, createSignal } from 'solid-js'
import { on as onPod, request } from '../pod/rpc'
import { mutate, project, session } from '../state/project'
import { showTool } from '../state/zones'
import { errorToast } from '../ui/toast'
import { setDynamicActions } from '../keys/bindings'
import { t } from '../i18n'
import { consoles, ordered, setConsoles, type ConsoleInfo } from './consoles'
import { openPreview } from '../llm/previews'

export interface Command {
  id: string
  name: string
  description?: string
  command: string
  flag: Flag
  cwd?: string
  port?: number
}

export const flags = ['serve', 'build', 'test', 'lint', 'setup', 'other'] as const
export type Flag = (typeof flags)[number]
export const flagIcon: Record<Flag, string> = { serve: 'screen', build: 'puzzle', test: 'check', lint: 'problems', setup: 'gear', other: 'play' }

const [commands, setCommands] = createSignal<Command[]>([])
/** Error of an invalid .ide/commands.json (the file is left as it is). */
const [fileError, setFileError] = createSignal('')
export { commands, fileError }

// One action per command (Search Everywhere, shortcuts of the keymap settings), bound by the id
// of the command: renaming it keeps its shortcut.
createRoot(() =>
  createEffect(() =>
    setDynamicActions(
      commands().map((c) => ({
        id: `command.run.${c.id}`,
        label: 'Run: {name}',
        params: { name: c.name },
        category: 'Project commands',
        inTerminal: true,
        icon: flagIcon[c.flag],
        detail: c.description || c.command.split('\n')[0],
        run: () => void runCommand(c),
      })),
    ),
  ),
)

export async function loadCommands() {
  try {
    const r = await request<{ commands: Command[]; error?: string }>('commands.list')
    setCommands(r.commands)
    setFileError(r.error ?? '')
  } catch {
    /* no project */
  }
}

onPod('commands.changed', (e: { commands: Command[] }) => {
  setCommands(e.commands)
  setFileError('')
})

export async function saveCommands(list: Command[]) {
  const saved = await request<Command[]>('commands.save', { commands: list })
  setCommands(saved)
  setFileError('')
  return saved
}

/** The console of a command in the worktree shown, while it runs. */
export function runningConsole(id: string) {
  return consoles().find((c) => c.commandId === id && !c.exited && c.project === project()?.id)
}

/** Runs a saved command; when it runs already, Restart is offered (same place in the tabs). */
export async function runCommand(cmd: Command, restart = false) {
  try {
    // The tabs as they are now: the console of a restart takes the place of the old one.
    const tabs = ordered().map((c) => c.id)
    let r = await request<{ running?: ConsoleInfo; console?: ConsoleInfo; replaced?: string }>('commands.run', { id: cmd.id, restart })
    if (r.running) {
      if (!confirm(t('“{name}” is running. Restart it?', { name: cmd.name }))) return show(r.running.id)
      r = await request('commands.run', { id: cmd.id, restart: true })
    }
    const info: ConsoleInfo = { ...r.console!, project: project()?.id }
    setConsoles((l) => [...l.filter((c) => c.id !== r.replaced && c.id !== info.id), info])
    mutate((s) => {
      // The new console takes the tab of the one it replaces.
      if (r.replaced && tabs.includes(r.replaced)) s.bottom.order = tabs.map((id) => (id === r.replaced ? info.id : id))
    })
    show(info.id)
    return info
  } catch (e) {
    errorToast(e)
  }
}

/** A serve command with a port: started (unless it runs) and opened as a preview. */
export const servesApp = (c: Command) => c.flag === 'serve' && !!c.port

/** Start and open the app: the pod runs the command in its console, waits for its port and
 * serves it on a temporary URL, opened in a new tab (see llm/previews.ts). */
export async function openApp(cmd: Command) {
  const p = project()
  if (!p) return
  try {
    const pv = await openPreview({ project: p.id, title: cmd.name, command: cmd.command, cwd: cmd.cwd, port: cmd.port ?? 0, commandId: cmd.id })
    // The console the pod just created may not be listed here yet.
    for (let i = 0; i < 20 && pv.console && !consoles().some((c) => c.id === pv.console); i++) await new Promise((r) => setTimeout(r, 100))
    if (consoles().some((c) => c.id === pv.console)) show(pv.console)
  } catch (e) {
    errorToast(e)
  }
}

function show(id: string) {
  mutate((s) => {
    showTool(s, 'console')
    s.bottom.active = id
  })
}

/** Command of the active console, for "Save as command" (an ad hoc task: sh -c <cmd>). */
export function adHocCommand(): string | null {
  const c = consoles().find((x) => x.id === session.bottom.active)
  if (!c || c.kind !== 'task' || c.commandId || c.command?.[0] !== 'sh' || c.command[1] !== '-c') return null
  return c.command[2] ?? null
}
