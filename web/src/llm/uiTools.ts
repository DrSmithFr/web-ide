// Tools of the assistant that act on the interface, run by this window when the pod asks it
// (assistant.ui): show a file, bring a panel, a console or the problems to the front, draw a page
// of the board (it needs the doodle code of the page: text measures, description, PNG).
import { attached, basename, loadDoc, mutate, openFile, openTab, relPath, root } from '../state/project'
import { errorToast, toast } from '../ui/toast'
import { openPreview, previewUrl, type PreviewSpec } from './previews'
import { doodleSession, openDoodle } from './doodle/session'
import { chat } from './state'
import { consoles } from '../console/consoles'
import { showTool, toolIds } from '../state/zones'
import { t } from '../i18n'
import { buildPage, type DrawArgs } from './board/build'
import { describe, MAX_SIDE, png } from './doodle/export'
import { pictureOf, type Picture } from './doodle/background'
import { newDoc } from './doodle/model'
import type { ModelPage } from './state'

interface UiResult {
  content: string
  summary: string
  status: 'ok' | 'error' | 'cancelled'
  page?: ModelPage
  /** Drawing of request_doodle: a data URL of a PNG. */
  image?: string
}

/** Root of the worktree the conversation shown works in (a ticket one may work in another worktree than the one shown). */
const chatRoot = () => attached[chat.ticket?.project ?? '']?.root ?? root()

/** Absolute path in the project of the conversation; "..", "." and duplicate slashes are resolved. */
export function absPath(p: string, base = chatRoot()): string {
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

const ok = (content: string, summary: string): UiResult => ({ content, summary, status: 'ok' })

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
      if (!toolIds().includes(id)) throw new Error(`unknown panel: ${id} (${toolIds().join(', ')})`)
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
 * A GIF kept as it is, so that it stays animated on the board (a canvas keeps its first image
 * only): when it needs no reduction. The PNG of the page shows its first image.
 */
async function gifPicture(blob: Blob, src: string): Promise<Picture | null> {
  if (blob.size > 4 << 20) return null
  const img = new Image()
  await new Promise((resolve, reject) => {
    img.onload = resolve
    img.onerror = () => reject(new Error('the GIF cannot be read'))
    img.src = src
  })
  return Math.max(img.naturalWidth, img.naturalHeight) <= 2048 ? { src, w: img.naturalWidth, h: img.naturalHeight } : null
}

/**
 * A page drawn for the model (board_draw_doodle, board_draw_image): built from the elements,
 * on an image (an SVG of the model, a capture of the screen) or a copy of a page; its
 * description and images.
 */
export async function drawPage(a: DrawArgs & { number: number; clone?: number; svg?: string; src?: string; origin?: string }, picture?: Picture, origin?: string): Promise<UiResult> {
  const title = String(a.title ?? '').trim() || t('Page {n}', { n: a.number })
  if (a.svg) {
    picture = await svgPicture(a.svg)
    origin = 'an SVG written by you'
  } else if (a.src) {
    // An image file of the project (an SVG file is drawn like an SVG of the model).
    const blob = await (await fetch(a.src)).blob()
    picture = blob.type === 'image/svg+xml' ? await svgPicture(await blob.text()) : ((blob.type === 'image/gif' && (await gifPicture(blob, a.src))) || (await pictureOf(blob)))
    origin = a.origin
  }
  const { doc, outside } = buildPage({ ...a, elements: a.elements ?? [], base: picture ? newDoc(picture) : undefined })
  let description = describe(doc, title, a.number, origin ? `${origin} (${picture!.w}×${picture!.h})` : undefined)
  if (a.clone) description += `\nA clone of page ${a.clone}, with your elements on top.`
  // Said for a blank page too: a model that meant to clone a page sees it did not.
  else if (!picture) description += '\nA new blank page (not a clone: give clone to draw on a page).'
  if (outside.length) description += `\nWarning: partly outside the frame: ${outside.join(', ')}.`
  const page: ModelPage = { name: title, doc, description, png: await png(doc, MAX_SIDE), thumb: await png(doc, 96) }
  return { ...ok(description, t('Page {n} · {name}', { n: a.number, name: title })), page }
}

/** base: root of the worktree the run works in (relative paths). */
export function runUiTool(tool: string, args: Record<string, any>, base?: string): Promise<UiResult> {
  switch (tool) {
    case 'board_draw':
      return drawPage(args as any)
    case 'open_file':
      return showFile(args.path, args.line, args.end_line, base)
    case 'open_diff':
      return showDiff(args.path, base)
    case 'show_message':
      return showMessage(args.text, args.kind)
    case 'share_preview':
      return offerPreview(args.spec)
    case 'request_doodle':
      return requestDoodle(args.title)
  }
  return focus(args, base)
}

// ---------- tools of the MCP (CLI agents) ----------

async function showDiff(p: string, base?: string): Promise<UiResult> {
  if (!p) throw new Error('path is missing')
  const abs = absPath(p, base)
  openTab({ kind: 'diff', path: abs, head: true, title: `${basename(abs)} (${t('changes')})` })
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
  const open = () =>
    void openPreview(spec)
      .then((p) => window.open(previewUrl(p), '_blank', 'noopener'))
      .catch(errorToast)
  toast(t('An agent offers to try “{title}”', { title: spec.title }), 'info', { label: t('Open'), run: open }, 120_000)
  return ok('Offered to the user.', spec.title)
}

/** request_doodle of an agent: the doodle editor, then the drawing as a PNG and its description. */
function requestDoodle(title: string): Promise<UiResult> {
  if (doodleSession()) return Promise.reject(new Error('the user is already drawing a doodle: ask again later'))
  const name = title || t('Doodle')
  return new Promise((resolve) => {
    let done = false
    openDoodle({
      doc: newDoc(),
      name,
      external: true,
      attachLabel: t('Send to the agent'),
      onAttach: async (doc) => {
        done = true
        resolve({ ...ok(describe(doc, name), name), image: await png(doc, MAX_SIDE) })
      },
      onClose: () => !done && resolve({ content: 'closed', summary: '', status: 'cancelled' }),
    })
  })
}
