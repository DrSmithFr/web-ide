// Web IDE in Claude Code, above the prompt: links to the project Claude works in and to
// the ticket of its worktree (at the public address of the IDE, publicUrl of the pod),
// and the files Claude changes as buttons opening them in the IDE windows (the /open
// endpoint of the pod, with its token). The band above the prompt is drawn by the
// terminal and the desktop app only: the mobile app gets the same links in a pane, opened
// when it joins the session and by /ide. /ide <file[:line]> opens any file.
//
// The pod: WEBIDE_URL (default http://127.0.0.1:4433), its data folder WEBIDE_DATA
// (default ~/.web-ide), where the token is.
import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { Ide, Ticket } from '../types'

const ide = atom({ plugin: 'web-ide', key: 'ide' } as const, null)
const files = atom({ plugin: 'web-ide', key: 'files' } as const, [])

const EDITS = new Set(['Edit', 'MultiEdit', 'Write', 'NotebookEdit'])
const MAX_FILES = 4
const PANE = 'web-ide'

type Pod = { url: string; token: string }

/** "# Ticket #12 · Export\nStatus: In progress · …\n…\nTicket in the IDE: <url>" → the ticket. */
export function parseTicket(markdown: string): Ticket | null {
  const [, id, title = '', status = ''] = /^# Ticket #(\d+) · (.*)\nStatus: ([^·\n]+)/.exec(markdown) ?? []
  const url = /^Ticket in the IDE: (\S+)$/m.exec(markdown)?.[1]
  return id && url ? { id: Number(id), title: title.trim(), status: status.trim(), url } : null
}

/** The page of the project in an answer of kanban_list or kanban_get. */
export function parseProject(text: string): string | null {
  return /^Project in the IDE: (\S+)$/m.exec(text)?.[1] ?? null
}

/** "src/a.go:12" → the absolute path and the line (0 without one). */
export function parseTarget(arg: string, cwd: string): { path: string; line: number } {
  const [, file = '', line] = /^(.*?)(?::(\d+))?$/.exec(arg.trim()) ?? []
  const path = file.startsWith('/') ? file : `${cwd.replace(/\/$/, '')}/${file.replace(/^\.\//, '')}`
  return { path, line: line ? Number(line) : 0 }
}

/** The files list after a change: the newest first, without duplicates. */
export function touched(list: string[], path: string): string[] {
  return [path, ...list.filter((p) => p !== path)].slice(0, MAX_FILES)
}

const basename = (p: string) => p.slice(p.lastIndexOf('/') + 1)

let pod: Pod | null = null

async function connect($: EngineInterface): Promise<Pod | null> {
  if (pod) return pod
  const home = (await $.env.get('HOME')) ?? ''
  const data = (await $.env.get('WEBIDE_DATA')) ?? `${home}/.web-ide`
  const url = (await $.env.get('WEBIDE_URL')) ?? 'http://127.0.0.1:4433'
  try {
    pod = { url: url.replace(/\/$/, ''), token: (await $.fs.read(`${data}/token`)).trim() }
  } catch {
    return null // no pod on this machine
  }
  return pod
}

/** Calls a tool of the MCP endpoint of the pod: its text, null on an error. */
async function tool($: EngineInterface, p: Pod, name: string, args: Record<string, unknown>): Promise<string | null> {
  const res = await $.http.fetch(`${p.url}/mcp`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${p.token}` },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name, arguments: args } }),
  })
  const r = JSON.parse(res.text)?.result
  return r && !r.isError ? (r.content?.[0]?.text ?? '') : null
}

/** The project Claude works in and the ticket of its worktree. */
async function refresh($: EngineInterface) {
  const p = await connect($)
  if (!p) return
  const cwd = await $.session.cwd()
  try {
    // In a worktree, kanban_get answers its ticket; elsewhere it fails and kanban_list
    // still gives the project (or fails too: not a project of the IDE).
    const text = (await tool($, p, 'kanban_get', { cwd })) ?? (await tool($, p, 'kanban_list', { cwd }))
    const project = text ? parseProject(text) : null
    const next: Ide | null = project ? { project, ticket: parseTicket(text ?? '') } : null
    await update($, ide, () => next)
  } catch {
    await update($, ide, () => null) // the pod is not running
  }
}

/** Opens a file in the windows of its project; answers what happened. */
async function open($: EngineInterface, path: string, line = 0): Promise<string> {
  const p = await connect($)
  if (!p) return 'Web IDE: no pod token found (~/.web-ide/token).'
  try {
    const res = await $.http.fetch(`${p.url}/open?path=${encodeURIComponent(path)}&line=${line}`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${p.token}` },
    })
    if (!res.ok) return `Web IDE: ${res.text.trim()}`
    const r = JSON.parse(res.text)
    return r.opened ? `Opened in the IDE: ${path}` : `No IDE window on this project: ${r.url}`
  } catch {
    return `Web IDE: the pod does not answer at ${p.url}.`
  }
}

export const register: Register = (on) => {
  on('session.start', async ($, e, next) => {
    await $.command.register({ name: 'ide', description: 'Links to the Web IDE (project, ticket, files), or open a file in it', argumentHint: '[file[:line]]' })
    void refresh($)
    return next(e)
  })

  // The mobile app draws no band above the prompt: the links go in a pane there.
  on('session.attach', { surface: 'mobile' }, async ($, e, next) => {
    void refresh($)
    void $.ui.open({ id: PANE, title: 'IDE' })
    return next(e)
  })

  on('turn.complete', async ($, e, next) => {
    void refresh($)
    return next(e)
  })

  on('tool.call', async ($, e, next) => {
    const ran = await next(e)
    const path = (e as { file_path?: string }).file_path ?? (e as { notebook_path?: string }).notebook_path
    if (EDITS.has(e.tool) && path && ran.deny === undefined) await update($, files, (list) => touched(list, path))
    return ran
  })

  on('command.run', { command: 'ide' }, async ($, e) => {
    if (e.args.trim()) {
      const t = parseTarget(e.args, await $.session.cwd())
      return { text: await open($, t.path, t.line) }
    }
    await refresh($)
    await $.ui.open({ id: PANE, title: 'IDE' })
    const here = await read($, ide)
    if (!here) return { text: 'This folder is not a project of the IDE.' }
    return { text: [`Project: ${here.project}`, ...(here.ticket ? [`Ticket #${here.ticket.id}: ${here.ticket.url}`] : [])].join('\n') }
  })

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e) => {
    const here = await read($, ide)
    const list = await read($, files)
    const { Box, Button, Link, Text } = $.ui.resolve(e)
    const tk = here?.ticket
    return (
      <Box flexDirection="column">
        {here ? <Link key="project" href={here.project} label="Project in the IDE" /> : <Text dimColor>This folder is not a project of the IDE.</Text>}
        {tk && <Link key="ticket" href={tk.url} label={`#${tk.id} ${tk.title} · ${tk.status}`} />}
        {list.map((path) => (
          <Button key={path} label={`↗ ${basename(path)}`} plain onPress={() => void open($, path).then((text) => $.ui.toast(text))} />
        ))}
      </Box>
    )
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    const here = await read($, ide)
    const list = await read($, files)
    if (e.props.hasSurvey || (!here && list.length === 0)) return next(e)
    const { Box, Button, Link, Text } = $.ui.resolve(e)
    const tk = here?.ticket
    return (
      <Box flexDirection="row" gap={1}>
        {here ? <Link key="project" href={here.project} label="IDE" /> : <Text dimColor>IDE</Text>}
        {tk && <Link key="ticket" href={tk.url} label={`#${tk.id} ${tk.title} · ${tk.status}`} />}
        {list.map((path) => (
          <Button key={path} label={`↗ ${basename(path)}`} plain dimColor onPress={() => void open($, path).then((text) => $.ui.toast(text))} />
        ))}
      </Box>
    )
  })
}
