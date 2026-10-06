// Tools of the agent that act on the interface, run by this window when the pod asks it
// (agent.ui): show a file, bring a panel, a console or the problems to the front, draw a page
// of the board (it needs the doodle code of the page: text measures, description, PNG).
import { loadDoc, mutate, openFile, relPath, root } from '../state/project'
import { consoles } from '../console/consoles'
import { showTool, toolIds } from '../state/zones'
import { t } from '../i18n'
import { buildPage, type DrawArgs } from './board/build'
import { describe, MAX_SIDE, png } from './doodle/export'
import type { ModelPage } from './state'

interface UiResult {
  content: string
  summary: string
  status: 'ok' | 'error'
  page?: ModelPage
}

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

const ok = (content: string, summary: string): UiResult => ({ content, summary, status: 'ok' })

async function showFile(p: string, line?: number, endLine?: number): Promise<UiResult> {
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

async function focus(a: Record<string, any>): Promise<UiResult> {
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
      // A console the pod just created may not be listed here yet.
      for (let i = 0; i < 20 && !consoles().some((x) => x.id === a.console_id); i++) await new Promise((r) => setTimeout(r, 100))
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

/** board_draw: the page built from the elements, its description and images. */
async function drawPage(a: DrawArgs & { number: number; from?: number }): Promise<UiResult> {
  const title = String(a.title ?? '').trim() || t('Page {n}', { n: a.number })
  const { doc, outside } = buildPage(a)
  let description = describe(doc, title, a.number)
  if (a.from) description += `\nA copy of page ${a.from}, with your elements on top.`
  if (outside.length) description += `\nWarning: partly outside the frame: ${outside.join(', ')}.`
  const page: ModelPage = { name: title, doc, description, png: await png(doc, MAX_SIDE), thumb: await png(doc, 96) }
  return { ...ok(description, t('Page {n} · {name}', { n: a.number, name: title })), page }
}

export function runUiTool(tool: string, args: Record<string, any>): Promise<UiResult> {
  if (tool === 'board_draw') return drawPage(args as any)
  return tool === 'open_file' ? showFile(args.path, args.line, args.end_line) : focus(args)
}
