// Saved commands of the project (Console ▶ menu): kept by the pod in .ide/commands.json of
// the main project, run as tasks in the worktree shown, one console per command.
import { createSignal } from 'solid-js'
import { on as onPod, request } from '../pod/rpc'
import { mutate, project, session } from '../state/project'
import { showTool } from '../state/zones'
import { errorToast } from '../ui/toast'
import { t } from '../i18n'
import { consoles, ordered, setConsoles, type ConsoleInfo } from './consoles'

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
