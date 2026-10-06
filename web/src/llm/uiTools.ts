// Tools of the agent that act on the interface, run by this window when the pod asks it
// (agent.ui): show a file, bring a panel, a console or the problems to the front, draw a page
// of the board (it needs the doodle code of the page: text measures, description, PNG).
import { loadDoc, mutate, openFile, relPath, root } from '../state/project'
import { consoles } from '../console/consoles'
import { showTool, toolIds } from '../state/zones'
import { t } from '../i18n'
import { buildPage, type DrawArgs } from './board/build'
import { describe, MAX_SIDE, png } from './doodle/export'
import type { Picture } from './doodle/background'
import { newDoc } from './doodle/model'
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

/** An SVG written by the model as an image: its size from width/height or the viewBox. */
function svgBlob(svg: string): Blob {
  const doc = new DOMParser().parseFromString(svg, 'image/svg+xml')
  const el = doc.documentElement
  if (el.nodeName !== 'svg') throw new Error('the SVG cannot be read')
  if (!el.getAttribute('xmlns')) el.setAttribute('xmlns', 'http://www.w3.org/2000/svg')
  const vb = (el.getAttribute('viewBox') ?? '').split(/[\s,]+/).map(Number)
  if (!parseFloat(el.getAttribute('width') ?? '') && vb.length === 4) el.setAttribute('width', String(vb[2]))
  if (!parseFloat(el.getAttribute('height') ?? '') && vb.length === 4) el.setAttribute('height', String(vb[3]))
  if (!parseFloat(el.getAttribute('width') ?? '') || !parseFloat(el.getAttribute('height') ?? '')) throw new Error('the SVG needs a width and a height, or a viewBox')
  return new Blob([new XMLSerializer().serializeToString(el)], { type: 'image/svg+xml' })
}

/** An SVG of the model as a picture, on a white ground (2048 px at most). */
async function svgPicture(svg: string): Promise<Picture> {
  const url = URL.createObjectURL(svgBlob(svg))
  try {
    const img = new Image()
    await new Promise((resolve, reject) => {
      img.onload = resolve
      img.onerror = () => reject(new Error('the SVG cannot be drawn'))
      img.src = url
    })
    const k = Math.min(1, 2048 / Math.max(img.naturalWidth, img.naturalHeight))
    const c = document.createElement('canvas')
    c.width = Math.max(1, Math.round(img.naturalWidth * k))
    c.height = Math.max(1, Math.round(img.naturalHeight * k))
    const g = c.getContext('2d')!
    g.fillStyle = '#fff'
    g.fillRect(0, 0, c.width, c.height)
    g.drawImage(img, 0, 0, c.width, c.height)
    return { src: c.toDataURL('image/png'), w: c.width, h: c.height }
  } finally {
    URL.revokeObjectURL(url)
  }
}

/**
 * A page drawn for the model (board_draw_doodle, board_draw_image): built from the elements,
 * on an image (an SVG of the model, a capture of the screen) or a copy of a page; its
 * description and images.
 */
export async function drawPage(a: DrawArgs & { number: number; from?: number; svg?: string }, picture?: Picture, origin?: string): Promise<UiResult> {
  const title = String(a.title ?? '').trim() || t('Page {n}', { n: a.number })
  if (a.svg) {
    picture = await svgPicture(a.svg)
    origin = 'an SVG written by you'
  }
  const { doc, outside } = buildPage({ ...a, elements: a.elements ?? [], base: picture ? newDoc(picture) : undefined })
  let description = describe(doc, title, a.number, origin ? `${origin} (${picture!.w}×${picture!.h})` : undefined)
  if (a.from) description += `\nA copy of page ${a.from}, with your elements on top.`
  if (outside.length) description += `\nWarning: partly outside the frame: ${outside.join(', ')}.`
  const page: ModelPage = { name: title, doc, description, png: await png(doc, MAX_SIDE), thumb: await png(doc, 96) }
  return { ...ok(description, t('Page {n} · {name}', { n: a.number, name: title })), page }
}

export function runUiTool(tool: string, args: Record<string, any>): Promise<UiResult> {
  if (tool === 'board_draw') return drawPage(args as any)
  return tool === 'open_file' ? showFile(args.path, args.line, args.end_line) : focus(args)
}
