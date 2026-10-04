// Tools the model can call: read and change the project files, search, and ask the
// language servers. They run in the page with the same RPCs as the rest of the IDE.
import { on as onPod, request } from '../pod/rpc'
import { activeTab, applyRemote, diagnostics, fileUri, flushLsp, getDoc, loadDoc, mutate, openFile, relPath, root, session } from '../state/project'
import { consoles, setConsoleList } from '../console/consoles'
import { refreshGit } from '../state/git'
import { showTool, toolIds } from '../state/zones'
import { lspLanguage, lspLanguageId } from '../editor/languages'
import { lineHunks } from '../editor/linediff'
import { flatten, symbolKinds, toLocations, type DocumentSymbol, type Location } from '../lsp/client'
import type { DiffLine, Mode, ToolCall } from './state'
import { askUserDef, kanbanReadDefs, kanbanToolNames, kanbanWriteDefs, runKanbanTool } from './kanbanTools'
import { runsFreely } from './commands'
import { t, tn } from '../i18n'

export interface ToolResult {
  /** Text given back to the model. */
  content: string
  /** One line shown folded in the conversation. */
  summary: string
  status: 'ok' | 'error' | 'denied'
  diff?: DiffLine[]
}

/** Asks the user before a change; resolves false when refused. */
export type Confirm = (req: { call: ToolCall; kind: 'edit' | 'command'; path?: string; diff?: DiffLine[]; created?: boolean; command?: string }) => Promise<boolean>

const str = (description: string) => ({ type: 'string', description })
const int = (description: string) => ({ type: 'integer', description })
const fn = (name: string, description: string, properties: Record<string, any>, required: string[] = []) => ({
  type: 'function',
  function: { name, description, parameters: { type: 'object', properties, required } },
})

const pathArg = str('Path relative to the project root (or absolute)')
const lineArg = int('Line number (starting at 1), as shown by read_file')
const symbolArg = str('The symbol as written on that line (function, variable, type name…)')

export const toolDefs = [
  fn('list_dir', 'Lists the content of a project folder.', { path: str('Folder, relative to the root ("" or "." for the root)') }),
  fn('find_files', 'Finds project files whose path contains the pattern (or matches the * ? glob).', { pattern: str('Ex. "handler", "*.go", "src/**/*.tsx"') }, ['pattern']),
  fn(
    'read_file',
    'Reads a project file. Each line is prefixed with its number and a tab (the prefix is not part of the file).',
    { path: pathArg, start_line: int('First line (1 by default)'), end_line: int('Last line, included') },
    ['path'],
  ),
  fn(
    'search_text',
    'Searches a text (or an RE2 regular expression) in all the project files.',
    { query: str('Text or expression'), regex: { type: 'boolean', description: 'query is a regular expression' }, include: str('File name globs, comma-separated, e.g. "*.go,*.ts"') },
    ['query'],
  ),
  fn(
    'edit_file',
    'Replaces an exact passage of a file. old_string must appear exactly once (without the line numbers of read_file): include enough context. To create a file, use write_file.',
    { path: pathArg, old_string: str('Exact text to replace'), new_string: str('Replacement text') },
    ['path', 'old_string', 'new_string'],
  ),
  fn('write_file', 'Creates a file or replaces its whole content.', { path: pathArg, content: str('Full content of the file') }, ['path', 'content']),
  fn('lsp_symbols', 'Structure of a file (classes, functions, methods…) from the language server.', { path: pathArg }, ['path']),
  fn('lsp_workspace_symbols', 'Finds a symbol by name in the whole project (language server).', { query: str('Name or start of the name'), path: str('A file of the language (optional: active file by default)') }, ['query']),
  fn('lsp_definition', 'Finds the definition of the symbol written on that line.', { path: pathArg, line: lineArg, symbol: symbolArg }, ['path', 'line', 'symbol']),
  fn('lsp_references', 'Finds the usages of the symbol written on that line.', { path: pathArg, line: lineArg, symbol: symbolArg }, ['path', 'line', 'symbol']),
  fn('lsp_hover', 'Type and documentation of the symbol written on that line.', { path: pathArg, line: lineArg, symbol: symbolArg }, ['path', 'line', 'symbol']),
  fn('lsp_diagnostics', 'Errors and warnings of the language server, for a file or for the open files.', { path: str('File (optional)') }),
  fn('load_skill', 'Loads the full instructions of a skill of the list (and the list of its other files).', { name: str('Skill name') }, ['name']),
  fn('read_skill_file', 'Reads another file of a skill (path relative to its folder, as listed by load_skill).', { name: str('Skill name'), file: str('Path of the file in the skill') }, ['name', 'file']),
  fn(
    'open_file',
    'Opens a file in the editor of the user to show it, optionally selecting lines.',
    { path: pathArg, line: int('First line to show (starting at 1)'), end_line: int('Last line to select') },
    ['path'],
  ),
  fn(
    'focus',
    'Brings an element of the IDE to the front: an open file, a panel (explorer, search, git, kanban, console, problems, database, assistant, structure, conflicts, info), a console or the problems list.',
    {
      target: { type: 'string', enum: ['file', 'panel', 'console', 'problems'], description: 'Kind of element' },
      path: str('File (target=file)'),
      panel: str('Panel (target=panel)'),
      console_id: str('Console id (target=console), see list_consoles'),
    },
    ['target'],
  ),
  fn(
    'bash',
    'Runs a shell command (sh -c, in the project) and returns its output (stdout and stderr) and exit code. Use it for all your operations: tests, builds, git, command-line tools. No terminal and no input: no interactive command.',
    { command: str('Command, e.g. "go test ./..."'), cwd: str('Working directory (project root by default)'), timeout: int('Timeout in seconds (120 by default, 1800 max)') },
    ['command'],
  ),
  fn(
    'run_command',
    'Runs a command in a new console of the IDE, visible to the user (development server, watcher, command they want to follow or use). Waits for its end or the timeout, then returns the start of its output; it keeps running afterwards. For your own operations, use bash.',
    { command: str('Command, e.g. "npm run dev"'), cwd: str('Working directory (project root by default)'), timeout: int('Seconds to wait before returning (20 by default, 600 max)') },
    ['command'],
  ),
  fn('list_consoles', 'Lists the open consoles (terminals and commands) with their state.', {}),
  fn('read_console', 'Reads the end of the output of a console.', { console_id: str('Console id'), lines: int('Number of lines (200 by default)') }, ['console_id']),
  fn(
    'console_input',
    'Types text in a console (interactive terminal, program waiting for an answer). A newline is added unless enter=false.',
    { console_id: str('Console id'), text: str('Text to type'), enter: { type: 'boolean', description: 'Press Enter after the text (true by default)' } },
    ['console_id', 'text'],
  ),
]

export const writeTools = new Set(['edit_file', 'write_file'])

/** Tools handled by the agent loop itself. */
export const agentToolDefs = {
  exitPlan: fn(
    'exit_plan_mode',
    'Presents the finished plan to the user (Plan mode). They can accept it, which switches to Build mode to carry it out, or ask for changes. After this call, wait for their answer.',
    { plan: str('The full plan in Markdown: goal, files, numbered steps, tests') },
    ['plan'],
  ),
  compact: fn(
    'compact_conversation',
    'Summarizes the older messages of the conversation to free context (the last exchange is kept). Use it when a task is done or the conversation gets long.',
    { instructions: str('What the summary must keep first (optional)') },
  ),
}

/**
 * Tools offered to the model in a mode: no file change in Plan and Briefing, exit_plan_mode
 * only in Plan. The kanban tools that change a ticket come with a conversation linked to a ticket.
 */
export function toolsFor(mode: Mode, ticket?: { id: number; role: string }) {
  const base = mode === 'build' ? toolDefs : toolDefs.filter((t) => !writeTools.has(t.function.name))
  const kanban = [...kanbanReadDefs, ...(ticket ? kanbanWriteDefs : []), askUserDef]
  // The briefing and the plan of a ticket end in the ticket itself, not in exit_plan_mode.
  const exit = mode === 'plan' && ticket?.role !== 'briefing' && ticket?.role !== 'plan' ? [agentToolDefs.exitPlan] : []
  return [...base, ...kanban, ...exit, agentToolDefs.compact]
}

const MAX_LINES = 1500
const MAX_CHARS = 120_000

/** Absolute path in the project; "..", "." and duplicate slashes are resolved. */
export function absPath(p: string): string {
  p = (p ?? '').trim()
  const base = p.startsWith('/') ? '' : root()
  const out: string[] = []
  for (const seg of `${base}/${p}`.split('/')) {
    if (!seg || seg === '.') continue
    if (seg === '..') out.pop()
    else out.push(seg)
  }
  return '/' + out.join('/')
}

function parseArgs(call: ToolCall): Record<string, any> {
  try {
    const v = JSON.parse(call.function.arguments || '{}')
    return v && typeof v === 'object' ? v : {}
  } catch {
    throw new Error(`invalid JSON arguments: ${call.function.arguments.slice(0, 200)}`)
  }
}

export async function runTool(call: ToolCall, confirm: Confirm, signal?: AbortSignal, mode: Mode = 'build', ticket?: number): Promise<ToolResult> {
  const name = call.function.name
  try {
    const a = parseArgs(call)
    if (kanbanToolNames.has(name)) return await runKanbanTool(name, a, ticket, mode)
    if (mode !== 'build') {
      if (writeTools.has(name))
        return fail(
          mode === 'plan'
            ? 'Plan mode: files cannot be changed. Present the plan with exit_plan_mode; it will be carried out in Build mode.'
            : 'Briefing mode: files cannot be changed. Clarify the need and write it in tickets (kanban_create).',
        )
      // A command that may change something waits for the user.
      if ((name === 'bash' || name === 'run_command') && !runsFreely(String(a.command ?? ''), root(), a.cwd ? absPath(a.cwd) : root())) {
        if (!(await confirm({ call, kind: 'command', command: String(a.command ?? '') })))
          return {
            content: `The user refused this command (${mode === 'plan' ? 'Plan' : 'Briefing'} mode: only reading commands, and the build and test commands of the project, run freely).`,
            summary: t('command refused'),
            status: 'denied',
          }
      }
    }
    switch (name) {
      case 'list_dir':
        return await listDir(a.path ?? '')
      case 'find_files':
        return await findFiles(String(a.pattern ?? ''))
      case 'read_file':
        return await readFile(a.path, a.start_line, a.end_line)
      case 'search_text':
        return await searchText(String(a.query ?? ''), !!a.regex, a.include ?? '')
      case 'edit_file':
        return await editFile(call, a.path, a.old_string, a.new_string, confirm)
      case 'write_file':
        return await writeFile(call, a.path, a.content, confirm)
      case 'lsp_symbols':
        return await lspSymbols(a.path)
      case 'lsp_workspace_symbols':
        return await lspWorkspaceSymbols(String(a.query ?? ''), a.path)
      case 'lsp_definition':
      case 'lsp_references':
      case 'lsp_hover':
        return await lspAt(name, a.path, Number(a.line), String(a.symbol ?? ''))
      case 'lsp_diagnostics':
        return await lspDiagnostics(a.path)
      case 'load_skill':
        return await loadSkill(String(a.name ?? ''))
      case 'read_skill_file':
        return await readSkillFile(String(a.name ?? ''), String(a.file ?? ''))
      case 'open_file':
        return await showFile(a.path, a.line, a.end_line)
      case 'focus':
        return await focus(a)
      case 'bash':
        return await bash(String(a.command ?? ''), a.cwd, a.timeout, signal)
      case 'run_command':
        return await runCommand(String(a.command ?? ''), a.cwd, a.timeout)
      case 'list_consoles':
        return listConsoles()
      case 'read_console':
        return await readConsole(String(a.console_id ?? ''), a.lines)
      case 'console_input':
        return await consoleInput(String(a.console_id ?? ''), String(a.text ?? ''), a.enter !== false)
    }
    return fail(`outil inconnu : ${name}`)
  } catch (e) {
    return fail((e as Error).message)
  }
}

function fail(message: string): ToolResult {
  return { content: `Error: ${message}`, summary: message, status: 'error' }
}

function ok(content: string, summary: string, diff?: DiffLine[]): ToolResult {
  return { content, summary, status: 'ok', diff }
}

function plural(n: number, word: string) {
  return `${n} ${word}${n === 1 ? '' : 's'}`
}

// ---------- files ----------

async function listDir(p: string): Promise<ToolResult> {
  const abs = absPath(p)
  const entries: { name: string; dir: boolean; size: number }[] = await request('fs.list', { path: abs })
  entries.sort((a, b) => Number(b.dir) - Number(a.dir) || a.name.localeCompare(b.name))
  const lines = entries.slice(0, 500).map((e) => (e.dir ? `${e.name}/` : `${e.name}  (${e.size} o)`))
  if (entries.length > 500) lines.push(`… ${entries.length - 500} more entries`)
  return ok(lines.join('\n') || '(empty folder)', `${abs === root() ? '.' : relPath(abs)}: ${tn(entries.length, '{n} entry', '{n} entries')}`)
}

function globToRegExp(glob: string): RegExp {
  let re = ''
  for (let i = 0; i < glob.length; i++) {
    const c = glob[i]
    if (c === '*') {
      if (glob[i + 1] === '*') {
        re += '.*'
        i++
        if (glob[i + 1] === '/') i++
      } else re += '[^/]*'
    } else if (c === '?') re += '[^/]'
    else re += c.replace(/[.+^${}()|[\]\\]/g, '\\$&')
  }
  return new RegExp(glob.includes('/') ? `^${re}$` : `(^|/)${re}$`, 'i')
}

async function findFiles(pattern: string): Promise<ToolResult> {
  const files: string[] = await request('search.files')
  let list: string[]
  if (/[*?]/.test(pattern)) {
    const re = globToRegExp(pattern)
    list = files.filter((f) => re.test(f))
  } else {
    const q = pattern.toLowerCase()
    list = files.filter((f) => f.toLowerCase().includes(q))
  }
  const shown = list.slice(0, 200)
  let text = shown.join('\n') || 'No file found.'
  if (list.length > shown.length) text += `\n… ${list.length - shown.length} more (refine the pattern)`
  return ok(text, `“${pattern}”: ${tn(list.length, '{n} file', '{n} files')}`)
}

async function readText(abs: string): Promise<string> {
  const f = await request('fs.read', { path: abs })
  if (f.binary) throw new Error(`${relPath(abs)} is a binary or too large file`)
  return f.content as string
}

async function readFile(p: string, start?: number, end?: number): Promise<ToolResult> {
  if (!p) throw new Error('path is missing')
  const abs = absPath(p)
  const text = await readText(abs)
  const lines = text.split('\n')
  const from = Math.max(1, Math.floor(start || 1))
  let to = Math.min(lines.length, Math.floor(end || lines.length))
  let out = ''
  let truncated = false
  for (let i = from; i <= to; i++) {
    const row = `${i}\t${lines[i - 1]}\n`
    if (i - from >= MAX_LINES || out.length + row.length > MAX_CHARS) {
      truncated = true
      to = i - 1
      break
    }
    out += row
  }
  if (truncated) out += `… (truncated: read on with start_line=${to + 1})\n`
  if (from > lines.length) out = `(the file has only ${lines.length} lines)`
  return ok(out, t('{path}: lines {from}-{to} of {total}', { path: relPath(abs), from, to, total: lines.length }))
}

async function searchText(query: string, regex: boolean, include: string): Promise<ToolResult> {
  if (!query) throw new Error('query is missing')
  const r = await request('search.grep', { query, regex, caseSensitive: false, wholeWord: false, include, max: 200 })
  const lines = (r.matches as { path: string; line: number; text: string }[]).map((m) => `${relPath(m.path)}:${m.line}: ${m.text.trim().slice(0, 300)}`)
  let text = lines.join('\n') || 'No result.'
  if (r.truncated) text += '\n… (results truncated, refine the search)'
  return ok(text, `“${query}”: ${tn(r.matches.length, '{n} result', '{n} results')}${r.truncated ? '+' : ''}`)
}

/** Unified diff of two texts with 2 lines of context, at most 400 lines. */
export function diffLines(oldText: string, newText: string): DiffLine[] {
  const a = oldText.split('\n')
  const b = newText.split('\n')
  const hunks = lineHunks(a, b)
  if (!hunks) return [{ t: '…', text: t('difference too large to show') }]
  const out: DiffLine[] = []
  const ctx = 2
  let lastA = -1
  for (const h of hunks) {
    const [as, al] = h.a
    const [bs, bl] = h.b
    const from = Math.max(as - ctx, lastA + 1, 0)
    if (lastA >= 0 && from > lastA + 1) out.push({ t: '…', text: '' })
    else if (lastA < 0 && from > 0) out.push({ t: '…', text: t('line {n}', { n: from + 1 }) })
    for (let i = from; i < as; i++) out.push({ t: ' ', text: a[i] })
    for (let i = as; i < as + al; i++) out.push({ t: '-', text: a[i] })
    for (let i = bs; i < bs + bl; i++) out.push({ t: '+', text: b[i] })
    const end = Math.min(as + al + ctx, a.length)
    for (let i = as + al; i < end; i++) out.push({ t: ' ', text: a[i] })
    lastA = end - 1
    if (out.length > 400) {
      out.length = 400
      out.push({ t: '…', text: t('rest of the diff hidden') })
      break
    }
  }
  return out
}

async function exists(abs: string) {
  try {
    await request('fs.stat', { path: abs })
    return true
  } catch {
    return false
  }
}

/** Writes the file, then updates the open editor (the pod does not echo our own writes). */
async function commit(abs: string, content: string) {
  const r = await request('fs.write', { path: abs, content })
  const doc = getDoc(abs)
  if (doc) applyRemote(doc, content, r.rev, true)
  refreshGit()
}

async function editFile(call: ToolCall, p: string, oldStr: string, newStr: string, confirm: Confirm): Promise<ToolResult> {
  if (!p) throw new Error('path is missing')
  if (typeof oldStr !== 'string' || typeof newStr !== 'string') throw new Error('old_string and new_string are required')
  if (oldStr === newStr) throw new Error('old_string and new_string are identical')
  const abs = absPath(p)
  const text = await readText(abs)
  let at = text.indexOf(oldStr)
  let old = oldStr
  if (at < 0 && text.includes('\r\n')) {
    // The model usually writes LF; the file uses CRLF.
    old = oldStr.replace(/\r?\n/g, '\r\n')
    newStr = newStr.replace(/\r?\n/g, '\r\n')
    at = text.indexOf(old)
  }
  if (at < 0) throw new Error(`old_string not found in ${relPath(abs)} (read the file again with read_file and copy the exact text, without the line numbers)`)
  if (text.indexOf(old, at + 1) >= 0) throw new Error(`old_string appears several times in ${relPath(abs)}: add context to make it unique`)
  const next = text.slice(0, at) + newStr + text.slice(at + old.length)
  const diff = diffLines(text, next)
  if (!(await confirm({ call, kind: 'edit', path: abs, diff, created: false }))) return { content: 'The user refused this change.', summary: t('change refused'), status: 'denied', diff }
  await commit(abs, next)
  const line = text.slice(0, at).split('\n').length
  const added = diff.filter((d) => d.t === '+').length
  const removed = diff.filter((d) => d.t === '-').length
  return ok(`Change applied to ${relPath(abs)} (line ${line}).`, `${relPath(abs)}: +${added} −${removed}`, diff)
}

async function writeFile(call: ToolCall, p: string, content: string, confirm: Confirm): Promise<ToolResult> {
  if (!p) throw new Error('path is missing')
  if (typeof content !== 'string') throw new Error('content is missing')
  const abs = absPath(p)
  const created = !(await exists(abs))
  const before = created ? '' : await readText(abs)
  if (!created && before === content) return ok(`${relPath(abs)} already has this content.`, t('{path}: unchanged', { path: relPath(abs) }))
  const diff = diffLines(before, content)
  if (created && diff[0]?.t === '-') diff.shift() // the empty line of "nothing"
  if (!(await confirm({ call, kind: 'edit', path: abs, diff, created }))) return { content: 'The user refused this write.', summary: t('write refused'), status: 'denied', diff }
  await commit(abs, content)
  const n = content.split('\n').length
  return ok(`${relPath(abs)} ${created ? 'created' : 'replaced'} (${plural(n, 'line')}).`, created ? tn(n, '{path} created ({n} line)', '{path} created ({n} lines)', { path: relPath(abs) }) : tn(n, '{path} replaced ({n} line)', '{path} replaced ({n} lines)', { path: relPath(abs) }), diff)
}

// ---------- language servers ----------

function langOf(abs: string) {
  const lang = lspLanguage(abs)
  if (!lang) throw new Error(`no language server for ${relPath(abs)}`)
  return lang
}

/** Runs fn with the file known to the language server (opened for the request if needed). */
async function withLspDoc<T>(abs: string, fn: (text: string) => Promise<T>): Promise<T> {
  const lang = langOf(abs)
  const doc = getDoc(abs)
  if (doc && !doc.readOnly) {
    flushLsp(abs)
    return fn(doc.text)
  }
  const text = await readText(abs)
  const uri = fileUri(abs)
  await request('lsp.notify', { lang, method: 'textDocument/didOpen', params: { textDocument: { uri, languageId: lspLanguageId(abs), version: 1, text } } })
  try {
    return await fn(text)
  } finally {
    request('lsp.notify', { lang, method: 'textDocument/didClose', params: { textDocument: { uri } } }).catch(() => {})
  }
}

function lspRequest<T = any>(abs: string, method: string, params: any): Promise<T> {
  return request('lsp.request', { lang: langOf(abs), method, params })
}

async function lspSymbols(p: string): Promise<ToolResult> {
  if (!p) throw new Error('path is missing')
  const abs = absPath(p)
  const symbols = await withLspDoc(abs, () => lspRequest<DocumentSymbol[] | null>(abs, 'textDocument/documentSymbol', { textDocument: { uri: fileUri(abs) } }))
  const list = symbols ?? []
  // Old servers answer SymbolInformation (flat, with a location).
  const rows = (list[0] as any)?.location
    ? (list as any[]).map((s) => `${symbolKinds[s.kind]?.[0] ?? '?'} ${s.name}${s.containerName ? ` (in ${s.containerName})` : ''} · line ${s.location.range.start.line + 1}`)
    : flatten(list).map(({ s, depth }) => `${'  '.repeat(depth)}${symbolKinds[s.kind]?.[0] ?? '?'} ${s.name}${s.detail ? ` ${s.detail}` : ''} · line ${s.selectionRange.start.line + 1}`)
  return ok(rows.join('\n') || 'No symbol.', `${relPath(abs)}: ${tn(rows.length, '{n} symbol', '{n} symbols')}`)
}

async function lspWorkspaceSymbols(query: string, p?: string): Promise<ToolResult> {
  const ref = p ? absPath(p) : activeTab()?.path
  if (!ref) throw new Error('give path: a file of the language')
  const res = await lspRequest<any[]>(ref, 'workspace/symbol', { query })
  const list = (res ?? []).slice(0, 100)
  const rows = list.map((s) => {
    const loc = s.location?.range ? `${relPath(decodeURIComponent(s.location.uri.replace(/^file:\/\//, '')))}:${s.location.range.start.line + 1}` : s.location?.uri ?? ''
    return `${symbolKinds[s.kind]?.[0] ?? '?'} ${s.name}${s.containerName ? ` (${s.containerName})` : ''} · ${loc}`
  })
  if ((res ?? []).length > list.length) rows.push(`… ${res.length - list.length} more`)
  return ok(rows.join('\n') || 'No symbol.', `“${query}”: ${tn((res ?? []).length, '{n} symbol', '{n} symbols')}`)
}

/** Column of the symbol in the line (whole word first). */
function columnOf(lineText: string, symbol: string): number {
  if (!symbol) return -1
  const esc = symbol.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const m = new RegExp(`(?<![\\w$])${esc}(?![\\w$])`).exec(lineText)
  if (m) return m.index
  return lineText.indexOf(symbol)
}

async function locationLines(locs: Location[]): Promise<string[]> {
  const cache = new Map<string, string[]>()
  const out: string[] = []
  for (const l of locs) {
    if (!cache.has(l.path)) {
      try {
        cache.set(l.path, (await readText(l.path)).split('\n'))
      } catch {
        cache.set(l.path, [])
      }
    }
    const text = (cache.get(l.path)![l.range.start.line] ?? '').trim().slice(0, 200)
    out.push(`${relPath(l.path)}:${l.range.start.line + 1}:${l.range.start.character + 1}  ${text}`)
  }
  return out
}

async function lspAt(name: string, p: string, line: number, symbol: string): Promise<ToolResult> {
  if (!p) throw new Error('path is missing')
  if (!line || line < 1) throw new Error('line is missing (starting at 1)')
  const abs = absPath(p)
  return withLspDoc(abs, async (text) => {
    const lineText = text.split('\n')[line - 1]
    if (lineText === undefined) throw new Error(`${relPath(abs)} has no line ${line}`)
    const col = columnOf(lineText, symbol)
    if (col < 0) throw new Error(`"${symbol}" is not on line ${line}: ${lineText.trim().slice(0, 200)}`)
    const position = { textDocument: { uri: fileUri(abs) }, position: { line: line - 1, character: col } }
    if (name === 'lsp_hover') {
      const res = await lspRequest<any>(abs, 'textDocument/hover', position)
      const part = (c: any): string => (typeof c === 'string' ? c : c?.value ?? '')
      const hover = !res?.contents ? '' : Array.isArray(res.contents) ? res.contents.map(part).join('\n\n') : part(res.contents)
      return ok(hover.trim() || 'No information.', `${symbol}: ${hover ? t('documentation') : t('nothing')}`)
    }
    const method = name === 'lsp_definition' ? 'textDocument/definition' : 'textDocument/references'
    const params = name === 'lsp_definition' ? position : { ...position, context: { includeDeclaration: false } }
    const locs = toLocations(await lspRequest(abs, method, params))
    const shown = await locationLines(locs.slice(0, 100))
    if (locs.length > shown.length) shown.push(`… ${locs.length - shown.length} more`)
    const def = name === 'lsp_definition'
    return ok(shown.join('\n') || (def ? 'No definition.' : 'No reference.'), `${symbol}: ${def ? tn(locs.length, '{n} definition', '{n} definitions') : tn(locs.length, '{n} reference', '{n} references')}`)
  })
}

const severities = ['', 'error', 'warning', 'info', 'hint']

function formatDiagnostics(path: string): string[] {
  return (diagnostics[path] ?? []).map((d) => `${relPath(path)}:${d.range.start.line + 1}:${d.range.start.character + 1} [${severities[d.severity ?? 1] ?? '?'}] ${d.message}${d.source ? ` (${d.source})` : ''}`)
}

async function lspDiagnostics(p?: string): Promise<ToolResult> {
  if (!p) {
    const rows = Object.keys(diagnostics).flatMap(formatDiagnostics)
    return ok(rows.slice(0, 300).join('\n') || 'No diagnostic in the open files.', tn(rows.length, '{n} diagnostic', '{n} diagnostics'))
  }
  const abs = absPath(p)
  let rows: string[]
  if (getDoc(abs)) rows = formatDiagnostics(abs)
  else {
    // Not open: open it in the server and read the diagnostics it publishes (before the
    // close, which clears them).
    const before = diagnostics[abs]
    rows = await withLspDoc(abs, async () => {
      const until = Date.now() + 4000
      while (Date.now() < until && diagnostics[abs] === before) await new Promise((r) => setTimeout(r, 150))
      return formatDiagnostics(abs)
    })
  }
  return ok(rows.join('\n') || `No diagnostic for ${relPath(abs)}.`, `${relPath(abs)}: ${tn(rows.length, '{n} diagnostic', '{n} diagnostics')}`)
}

// ---------- skills ----------

async function loadSkill(name: string): Promise<ToolResult> {
  if (!name) throw new Error('name is missing')
  const s = await request('llm.skill.read', { name })
  let text = s.content as string
  if (s.files?.length) text += `\n\n---\nOther files of the skill (read_skill_file):\n${(s.files as string[]).map((f) => `- ${f}`).join('\n')}`
  return ok(text, t('skill {name} loaded', { name }))
}

async function readSkillFile(name: string, file: string): Promise<ToolResult> {
  if (!name || !file) throw new Error('name and file are required')
  const text: string = await request('llm.skill.file', { name, file })
  return ok(text, `${name}/${file}`)
}

// ---------- IDE ----------

async function showFile(p: string, line?: number, endLine?: number): Promise<ToolResult> {
  if (!p) throw new Error('path is missing')
  const abs = absPath(p)
  if (!line) {
    await openFile(abs)
    return ok(`${relPath(abs)} opened in the editor.`, t('{path} opened', { path: relPath(abs) }))
  }
  const doc = await loadDoc(abs)
  if (!doc) throw new Error(`${relPath(abs)} cannot be opened (binary, too large or missing)`)
  const l1 = Math.min(Math.max(1, Math.floor(line)), doc.lineCount) - 1
  const l2 = Math.min(Math.max(l1 + 1, Math.floor(endLine || line)), doc.lineCount) - 1
  await openFile({ path: abs, offset: doc.lineStart(l1), end: endLine ? doc.lineEnd(l2) : doc.lineStart(l1) })
  const range = endLine ? `lines ${l1 + 1}-${l2 + 1}` : `line ${l1 + 1}`
  return ok(`${relPath(abs)} opened in the editor, ${range}.`, `${relPath(abs)} · ${endLine ? t('lines {from}-{to}', { from: l1 + 1, to: l2 + 1 }) : t('line {n}', { n: l1 + 1 })}`)
}

async function focus(a: Record<string, any>): Promise<ToolResult> {
  switch (a.target) {
    case 'file': {
      if (!a.path) throw new Error('path is missing')
      await openFile(absPath(a.path))
      return ok(`${relPath(absPath(a.path))} brought to the front.`, relPath(absPath(a.path)))
    }
    case 'panel': {
      const id = String(a.panel ?? '')
      if (!toolIds.includes(id)) throw new Error(`unknown panel: ${id} (${toolIds.join(', ')})`)
      mutate((s) => showTool(s, id))
      return ok(`Panel ${id} shown.`, t('panel {id}', { id }))
    }
    case 'console': {
      const c = consoles().find((x) => x.id === a.console_id)
      if (!c) throw new Error(`console not found: ${a.console_id} (see list_consoles)`)
      mutate((s) => {
        showTool(s, 'console')
        s.bottom.active = c.id
      })
      return ok(`Console "${c.title}" shown.`, `console ${c.title}`)
    }
    case 'problems':
      mutate((s) => {
        showTool(s, 'problems')
        s.bottom.problemsTab = 'problems'
      })
      return ok('Problems list shown.', t('problems'))
  }
  throw new Error('target must be file, panel, console or problems')
}

// ---------- consoles ----------

/** Terminal output as plain text: escape sequences removed, carriage returns applied. */
export function plainOutput(raw: string): string {
  const text = raw
    // eslint-disable-next-line no-control-regex
    .replace(/\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g, '')
    // eslint-disable-next-line no-control-regex
    .replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, '')
    // eslint-disable-next-line no-control-regex
    .replace(/\x1b[()][A-Za-z0-9]|\x1b[=>78NOM]/g, '')
    .replace(/\r\n/g, '\n')
  return text
    .split('\n')
    .map((l) => (l.includes('\r') ? l.slice(l.lastIndexOf('\r', l.length - 2) + 1).replace(/\r$/, '') : l))
    .join('\n')
}

function decodeB64(s: string) {
  return new TextDecoder().decode(Uint8Array.from(atob(s), (c) => c.charCodeAt(0)))
}

async function consoleText(id: string): Promise<{ text: string; info: any }> {
  const r = await request('console.attach', { id })
  return { text: plainOutput(decodeB64(r.data)), info: r.info }
}

function tail(text: string, lines: number, chars = 12_000) {
  let t = text.replace(/\s+$/, '')
  const all = t.split('\n')
  let cut = false
  if (all.length > lines) {
    t = all.slice(-lines).join('\n')
    cut = true
  }
  if (t.length > chars) {
    t = t.slice(-chars)
    cut = true
  }
  return (cut ? '… (start cut)\n' : '') + t
}

const exits = new Map<string, number>()
onPod('console.exit', (e: { id: string; code: number }) => exits.set(e.id, e.code))

async function runCommand(command: string, cwd?: string, timeout?: number): Promise<ToolResult> {
  if (!command.trim()) throw new Error('command is missing')
  const limit = Math.min(Math.max(Number(timeout) || 20, 1), 600) * 1000
  const info = await request('console.create', {
    kind: 'task',
    title: command.length > 60 ? command.slice(0, 57) + '…' : command,
    command: ['sh', '-c', command],
    cwd: cwd ? absPath(cwd) : undefined,
    cols: 160,
    rows: 40,
  })
  setConsoleList([...consoles().filter((c) => c.id !== info.id), info])
  mutate((s) => {
    showTool(s, 'console')
    s.bottom.active = info.id
  })
  const until = Date.now() + limit
  while (!exits.has(info.id) && Date.now() < until) await new Promise((r) => setTimeout(r, 150))
  const { text, info: now } = await consoleText(info.id)
  const code = exits.get(info.id) ?? (now?.exited ? now.code : undefined)
  const out = tail(text, 300)
  if (code === undefined) {
    return ok(`The command is still running (console ${info.id}). Output so far:\n${out}\n\nFollow it with read_console, interact with console_input.`, t('{command}: still running', { command }))
  }
  exits.delete(info.id)
  return {
    content: `Exit code: ${code} (console ${info.id})\n${out || '(no output)'}`,
    summary: `code ${code}`,
    status: code === 0 ? 'ok' : 'error',
  }
}

function listConsoles(): ToolResult {
  const list = consoles()
  const rows = list.map((c) => `${c.id} · ${c.kind === 'task' ? 'command' : 'terminal'} · ${c.title}${c.exited ? ` · exited (code ${c.code})` : ' · running'}${session.bottom.active === c.id ? ' · shown' : ''}`)
  return ok(rows.join('\n') || 'No open console.', tn(list.length, '{n} console', '{n} consoles'))
}

async function readConsole(id: string, lines?: number): Promise<ToolResult> {
  if (!id) throw new Error('console_id is missing')
  const { text, info } = await consoleText(id)
  const state = info?.exited ? `exited (code ${info.code})` : 'running'
  return ok(`Console "${info?.title ?? id}", ${state}:\n${tail(text, Math.max(1, Number(lines) || 200)) || '(no output)'}`, `${info?.title ?? id} · ${info?.exited ? t('exited (code {code})', { code: info.code }) : t('running')}`)
}

async function consoleInput(id: string, text: string, enter: boolean): Promise<ToolResult> {
  if (!consoles().some((c) => c.id === id)) throw new Error(`console not found: ${id}`)
  await request('console.input', { id, data: text + (enter ? '\r' : '') })
  await new Promise((r) => setTimeout(r, 800))
  const { text: out } = await consoleText(id)
  return ok(`Text sent. End of the output:\n${tail(out, 40)}`, t('typed in {id}', { id }))
}

// ---------- bash ----------

async function bash(command: string, cwd?: string, timeout?: number, signal?: AbortSignal): Promise<ToolResult> {
  if (!command.trim()) throw new Error('command is missing')
  const r = await request('exec.run', { command, cwd: cwd ? absPath(cwd) : '', timeout: Number(timeout) || 120 }, signal)
  const secs = (r.durationMs / 1000).toFixed(1)
  const out = plainOutput(r.output ?? '').replace(/\s+$/, '')
  const state = r.canceled ? 'canceled' : r.timedOut ? `stopped after the timeout (${secs} s)` : `exit code ${r.code}`
  return {
    content: `${state[0].toUpperCase() + state.slice(1)}${r.truncated ? ' (output cut in the middle)' : ''}\n${out || '(no output)'}`,
    summary: r.timedOut ? t('timeout') : r.canceled ? t('canceled') : `code ${r.code} · ${secs} s`,
    status: r.code === 0 && !r.timedOut && !r.canceled ? 'ok' : 'error',
  }
}
