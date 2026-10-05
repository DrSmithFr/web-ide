// Web IDE in Claude Code: the ticket of the worktree Claude works in, above the prompt,
// and the files Claude changes as buttons opening them in the IDE (the /open endpoint of
// the pod, with its token). /ide <file[:line]> opens any file.
//
// The pod: WEBIDE_URL (default http://127.0.0.1:4433), its data folder WEBIDE_DATA
// (default ~/.web-ide), where the token is.
import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { Ticket } from '../types'

const ticket = atom({ plugin: 'web-ide', key: 'ticket' } as const, null)
const files = atom({ plugin: 'web-ide', key: 'files' } as const, [])

const EDITS = new Set(['Edit', 'MultiEdit', 'Write', 'NotebookEdit'])
const MAX_FILES = 4

type Pod = { url: string; token: string }

/** "# Ticket #12 · Export\nStatus: In progress · …" → the ticket. */
export function parseTicket(markdown: string): Ticket | null {
  const [, id, title = '', status = ''] = /^# Ticket #(\d+) · (.*)\nStatus: ([^·\n]+)/.exec(markdown) ?? []
  return id ? { id: Number(id), title: title.trim(), status: status.trim() } : null
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

/** The ticket of the worktree Claude works in, asked to the MCP endpoint of the pod. */
async function refresh($: EngineInterface) {
  const p = await connect($)
  if (!p) return
  const cwd = await $.session.cwd()
  try {
    const res = await $.http.fetch(`${p.url}/mcp`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${p.token}` },
      body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: 'kanban_get', arguments: { cwd } } }),
    })
    const r = JSON.parse(res.text)?.result
    const tk = r && !r.isError ? parseTicket(r.content?.[0]?.text ?? '') : null
    await update($, ticket, () => tk)
  } catch {
    await update($, ticket, () => null) // the pod is not running
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
    await $.command.register({ name: 'ide', description: 'Open a file in the Web IDE (default: the last file changed)', argumentHint: '[file[:line]]' })
    void refresh($)
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
    const [last] = await read($, files)
    return { text: last ? await open($, last) : 'Usage: /ide <file[:line]> (no file changed yet)' }
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    const tk = await read($, ticket)
    const list = await read($, files)
    if (e.props.hasSurvey || (!tk && list.length === 0)) return next(e)
    const { Box, Button, Text } = $.ui.resolve(e)
    return (
      <Box flexDirection="row" gap={1}>
        <Text dimColor>{tk ? `IDE #${tk.id} ${tk.title} · ${tk.status}` : 'IDE'}</Text>
        {list.map((path) => (
          <Button key={path} label={`↗ ${basename(path)}`} plain dimColor onPress={() => void open($, path).then((text) => $.ui.toast(text))} />
        ))}
      </Box>
    )
  })
}
