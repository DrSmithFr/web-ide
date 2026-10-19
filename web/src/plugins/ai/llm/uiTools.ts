// The interface tools of the AI, run by this window when the pod asks it (ui.call, through
// ui/uiCalls.ts): draw a page of the board (it needs the doodle code of the page: text
// measures, description, PNG) and ask the user a doodle (request_doodle of the MCP).
import { doodleSession, openDoodle } from './doodle/session'
import { chat } from './state'
import { buildPage, type DrawArgs } from './board/build'
import { describe, MAX_SIDE, png } from './doodle/export'
import { pictureOf, type Picture } from './doodle/background'
import { newDoc } from './doodle/model'
import type { ModelPage } from './state'
import { resolvePath, root, rootOf, t } from '../plugin'
import type { CoreResult } from '../plugin'

const ok = (content: string, summary: string) => ({ content, summary, status: 'ok' as const })

type UiResult = Omit<CoreResult, 'page'> & { page?: ModelPage }

/** Root of the worktree the conversation shown works in (a ticket one may work in another worktree than the one shown). */
const chatRoot = () => rootOf(chat.ticket?.project ?? '') ?? root()

/** Absolute path in the project of the conversation (ui/uiCalls.ts resolvePath). */
export const absPath = (p: string, base = chatRoot()) => resolvePath(p, base)


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

/** The interface tools the AI adds to those of the core (uiTools contribution). */
export const aiUiTools = {
  board_draw: (args: Record<string, any>) => drawPage(args as any),
  request_doodle: (args: Record<string, any>) => requestDoodle(args.title),
}
