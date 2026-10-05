// What the model receives for a doodle: a PNG of the frame (light colors on white) and a text
// description built from the elements, which gives the proportions an image does not.
import type { Caps, Part } from '../state'
import type { Prepared } from '../attachments'
import { bounds, colorName, presets, type DoodleDoc, type Element, type Frame } from './model'
import { toSVG } from './render'

const MAX_SIDE = 1600
const MAX_LISTED = 30

function svgImage(svg: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const img = new Image()
    img.onload = () => resolve(img)
    img.onerror = () => reject(new Error('doodle export failed'))
    img.src = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svg)
  })
}

async function png(doc: DoodleDoc, max: number): Promise<string> {
  const { w, h } = doc.frame
  const scale = Math.min(2, max / Math.max(w, h))
  const cw = Math.max(1, Math.round(w * scale))
  const ch = Math.max(1, Math.round(h * scale))
  const img = await svgImage(toSVG(doc, cw, ch))
  const c = document.createElement('canvas')
  c.width = cw
  c.height = ch
  c.getContext('2d')!.drawImage(img, 0, 0, cw, ch)
  return c.toDataURL('image/png')
}

const pct = (v: number) => Math.round(v * 100)

/** Part of the element inside the frame, in fractions of the frame; null when outside. */
function inFrame(b: Frame, fr: Frame): Frame | null {
  const x0 = Math.max(b.x, fr.x)
  const y0 = Math.max(b.y, fr.y)
  const x1 = Math.min(b.x + b.w, fr.x + fr.w)
  const y1 = Math.min(b.y + b.h, fr.y + fr.h)
  if (x1 <= x0 || y1 <= y0) return null
  return { x: (x0 - fr.x) / fr.w, y: (y0 - fr.y) / fr.h, w: (x1 - x0) / fr.w, h: (y1 - y0) / fr.h }
}

function area(r: Frame): string {
  const cx = r.x + r.w / 2
  const cy = r.y + r.h / 2
  const v = cy < 1 / 3 ? 'top' : cy > 2 / 3 ? 'bottom' : 'middle'
  const hz = cx < 1 / 3 ? 'left' : cx > 2 / 3 ? 'right' : 'center'
  return v === 'middle' && hz === 'center' ? 'center' : `${v} ${hz}`
}

function strokes(list: { el: Element; r: Frame }[], label: string): string[] {
  if (!list.length) return []
  const lines = [`${label}: ${list.length}`]
  for (const { el, r } of list.slice(0, MAX_LISTED)) {
    lines.push(`- ${colorName(el)}, ${area(r)} (x ${pct(r.x)}–${pct(r.x + r.w)} %, y ${pct(r.y)}–${pct(r.y + r.h)} %)`)
  }
  if (list.length > MAX_LISTED) lines.push(`- … ${list.length - MAX_LISTED} more`)
  return lines
}

/** Text read by the model (always English, like the rest of what it reads). */
export function describe(doc: DoodleDoc, name: string): string {
  const fr = doc.frame
  const preset = doc.preset === 'free' ? 'free ratio' : presets.find((p) => p.id === doc.preset)?.label
  const seen = doc.elements.map((el) => ({ el, r: inFrame(bounds(el), fr) })).filter((x): x is { el: Element; r: Frame } => !!x.r)
  const lines = [
    `Doodle "${name}" drawn by the user. Frame ${Math.round(fr.w)}×${Math.round(fr.h)} (${preset}); positions in % of the frame from its top left corner.`,
    ...strokes(seen.filter((x) => x.el.type === 'pen'), 'Free pen strokes (hand drawn, shapes approximate)'),
    ...strokes(seen.filter((x) => x.el.type === 'marker'), 'Highlighter strokes (emphasis)'),
  ]
  if (!seen.length) lines.push('The frame is empty.')
  return lines.join('\n')
}

/** Attachment of a doodle: image and description, or the description alone for a model without images. */
export async function prepareDoodle(doc: DoodleDoc, name: string, caps: Caps | undefined): Promise<Prepared> {
  const image = await png(doc, MAX_SIDE)
  const thumb = await png(doc, 96)
  const text = describe(doc, name)
  const vision = !(caps?.known && !caps.vision)
  const parts: Part[] = vision ? [{ type: 'image_url', image_url: { url: image } }, { type: 'text', text }] : [{ type: 'text', text }]
  return {
    parts,
    attachment: { name, kind: 'doodle', size: Math.round((image.length * 3) / 4), thumb, doodle: structuredClone(doc), description: text },
  }
}
