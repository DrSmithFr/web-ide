// Tools that act on the interface, run by this window when the pod asks it (ui.call): show a
// file, a diff or a message, bring a panel, a console or the problems to the front, offer an app
// preview. The tools of the MCP endpoint (Claude Code, the agents) and of the assistant use them;
// a plugin adds its own (uiTools: the AI draws a page of its board, asks a doodle).
import { attachWorktree, basename, loadDoc, mutate, openFile, openTab, relPath, root } from '../state/project'
import { on, request } from '../pod/rpc'
import { errorToast, toast } from './toast'
import { capability, type PreviewSpec } from '../plugins/capabilities'
import { showTool, toolIds } from '../state/zones'
import { tileTools } from '../state/tiles'
import { t } from '../i18n'

export interface UiResult {
  content: string
  summary: string
  status: 'ok' | 'error' | 'cancelled'
  /** A page of the board drawn for the model (the AI's). */
  page?: unknown
  /** Drawing of request_doodle: a data URL of a PNG. */
  image?: string
}

export type UiTool = (args: Record<string, any>, base?: string) => Promise<UiResult>

/** Absolute path of a path given by a model, from base (the root of the project by default); "..", "." and duplicate slashes are resolved. */
export function resolvePath(p: string, base = root()): string {
  p = (p ?? '').trim()
  if (p.startsWith('/')) base = ''
  const out: string[] = []
  for (const seg of `${base}/${p}`.split('/')) {
    if (!seg || seg === '.') continue
    if (seg === '..') out.pop()
    else out.push(seg)
  }
  return '/' + out.join('/')
}

const absPath = resolvePath

export const ok = (content: string, summary: string) => ({ content, summary, status: 'ok' as const })

async function showFile(p: string, line?: number, endLine?: number, base?: string): Promise<UiResult> {
  if (!p) throw new Error('path is missing')
  const abs = absPath(p, base)
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

async function focus(a: Record<string, any>, base?: string): Promise<UiResult> {
  switch (a.target) {
    case 'file': {
      if (!a.path) throw new Error('path is missing')
      const abs = absPath(a.path, base)
      await openFile(abs)
      return ok(`${relPath(abs)} brought to the front.`, relPath(abs))
    }
    case 'panel': {
      const id = String(a.panel ?? '')
      const shown = toolIds().filter((x) => tileTools[x])
      if (!shown.includes(id)) throw new Error(`unknown panel: ${id} (${shown.join(', ')})`)
      mutate((s) => showTool(s, id))
      return ok(`Panel ${id} shown.`, t('panel {id}', { id }))
    }
    case 'console': {
      const term = capability('terminal')
      if (!term) throw new Error('the consoles are off on this project (the Exec plugin)')
      // A console the pod just created may not be listed here yet.
      for (let i = 0; i < 20 && !term.consoles().some((x) => x.id === a.console_id); i++) await new Promise((r) => setTimeout(r, 100))
      const c = term.consoles().find((x) => x.id === a.console_id)
      if (!c) throw new Error(`console not found: ${a.console_id} (see list_consoles)`)
      term.show(c.id)
      return ok(`Console "${c.title}" shown.`, `console ${c.title}`)
    }
    case 'problems': {
      const lang = capability('language')
      if (!lang) throw new Error('the language servers are off on this project (the Language servers plugin)')
      lang.showProblems()
      return ok('Problems list shown.', t('problems'))
    }
  }
  throw new Error('target must be file, panel, console or problems')
}

// ---------- tools of the MCP (CLI agents) ----------

async function showDiff(p: string, base?: string): Promise<UiResult> {
  if (!p) throw new Error('path is missing')
  const abs = absPath(p, base)
  if (!capability('git')) throw new Error('the Git plugin is off on this project: no diff to show')
  openTab({ kind: 'diff', source: 'git', path: abs, head: true, title: `${basename(abs)} (${t('changes')})` })
  return ok(`Changes of ${relPath(abs)} shown in the diff view.`, relPath(abs))
}

async function showMessage(text: string, kind?: string): Promise<UiResult> {
  if (!text) throw new Error('text is missing')
  const k = kind === 'ok' || kind === 'warn' || kind === 'error' ? kind : 'info'
  toast(text, k, undefined, 8000)
  return ok('Message shown to the user.', text)
}

/** share_preview of an agent: a notification whose button starts the app (nothing before). */
async function offerPreview(spec: PreviewSpec): Promise<UiResult> {
  const pv = capability('previews')
  if (!pv) throw new Error('the previews are off on this project (the Previews plugin)')
  const open = () =>
    void pv
      .open(spec)
      .then((p) => window.open(pv.url(p), '_blank', 'noopener'))
      .catch(errorToast)
  toast(t('An agent offers to try “{title}”', { title: spec.title }), 'info', { label: t('Open'), run: open }, 120_000)
  return ok('Offered to the user.', spec.title)
}

let pluginTools: Record<string, UiTool> = {}
/** Set by the plugin registry: the interface tools of the enabled plugins. */
export function setPluginUiTools(tools: Record<string, UiTool>) {
  pluginTools = tools
}

/** base: root of the worktree the run works in (relative paths). */
export function runUiTool(tool: string, args: Record<string, any>, base?: string): Promise<UiResult> {
  switch (tool) {
    case 'open_file':
      return showFile(args.path, args.line, args.end_line, base)
    case 'open_diff':
      return showDiff(args.path, base)
    case 'show_message':
      return showMessage(args.text, args.kind)
    case 'share_preview':
      return offerPreview(args.spec)
  }
  if (pluginTools[tool]) return pluginTools[tool](args, base)
  if (tool !== 'focus') return Promise.reject(new Error(`${tool} is off on this project (its plugin is disabled)`))
  return focus(args, base)
}

/** A tool call that acts on the interface: this window runs it and answers with ui.result. */
on('ui.call', async (req: { id: string; chat: string; tool: string; args: Record<string, any>; project?: string }) => {
  // The run may work in another worktree than the one shown: its files open from there.
  const base = req.project ? await attachWorktree(req.project).then((w) => w.root, () => undefined) : undefined
  const res = await runUiTool(req.tool, req.args, base).catch((e) => ({ content: `Error: ${(e as Error).message}`, summary: (e as Error).message, status: 'error' as const }))
  if (!req.args.quiet) request('ui.result', { id: req.id, ...res }).catch(() => {})
})
