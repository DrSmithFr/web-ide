// What the model receives for a doodle: a PNG of the frame (light colors on white) and a text
// description built from the elements, which gives the proportions an image does not.
import type { Caps, Part } from '../state'
import type { Prepared } from '../attachments'
import { bounds, colorName, contains, isShape, presets, zoneAt, type DoodleDoc, type Element, type Frame, type Layout, type Zone } from './model'
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

function place(r: Frame): string {
  return `${area(r)} (x ${pct(r.x)}–${pct(r.x + r.w)} %, y ${pct(r.y)}–${pct(r.y + r.h)} %)`
}

function strokes(list: { el: Element; r: Frame }[], label: string): string[] {
  if (!list.length) return []
  const lines = [`${label}: ${list.length}`]
  for (const { el, r } of list.slice(0, MAX_LISTED)) lines.push(`- ${colorName(el)}, ${place(r)}`)
  if (list.length > MAX_LISTED) lines.push(`- … ${list.length - MAX_LISTED} more`)
  return lines
}

const quote = (s: string) => JSON.stringify(s.replace(/\s+/g, ' ').trim())
const sizeNames = { s: 'small', m: 'medium', l: 'large' }

/** Shapes and texts, numbered so that arrows and containers can refer to them. */
function items(list: { el: Element; r: Frame }[], fr: Frame): string[] {
  const shown = list.filter((x) => x.el.type === 'text' || x.el.type === 'layout' || isShape(x.el))
  if (!shown.length) return []
  const num = new Map(shown.map((x, i) => [x.el.id, i + 1]))
  const frac = (x: number, y: number) => ({ x: (x - fr.x) / fr.w, y: (y - fr.y) / fr.h })
  // The smallest closed shape around a point or a box.
  const around = (b: Frame, self: string) => {
    let best: { el: Element; area: number } | null = null
    for (const { el } of shown) {
      if (el.id === self || (el.type !== 'rect' && el.type !== 'ellipse' && el.type !== 'layout')) continue
      const o = bounds(el)
      if (!contains({ x: o.x - 12, y: o.y - 12, w: o.w + 24, h: o.h + 24 }, b)) continue
      if (!best || o.w * o.h < best.area) best = { el, area: o.w * o.h }
    }
    return best?.el
  }
  // The smallest shape or text an end of an arrow touches.
  const at = (x: number, y: number) => {
    let best: { el: Element; area: number } | null = null
    for (const { el } of shown) {
      if (el.type === 'line' || el.type === 'arrow' || !hitBox(el, x, y)) continue
      const o = bounds(el)
      if (!best || o.w * o.h < best.area) best = { el, area: o.w * o.h }
    }
    return best?.el
  }
  // A layout names the zone of what it holds.
  const ref = (el: Element | undefined, b?: Frame) => {
    if (!el) return ''
    const z = el.type === 'layout' && b ? zoneAt(el, b.x + b.w / 2, b.y + b.h / 2)?.zone.name : undefined
    return `[${num.get(el.id)}]${z ? ` zone ${quote(z)}` : ''}`
  }
  const lines = ['Shapes and texts:']
  for (const { el, r } of shown.slice(0, MAX_LISTED * 2)) {
    const n = `[${num.get(el.id)}]`
    if (el.type === 'text') {
      const inside = around(bounds(el), el.id)
      lines.push(`${n} text ${quote(el.text)}, ${sizeNames[el.size]}, ${colorName(el)}, ${place(r)}${inside ? `, inside ${ref(inside, bounds(el))}` : ''}`)
    } else if (el.type === 'layout') {
      lines.push(...layoutLines(el, n, r))
    } else if (el.type === 'line' || el.type === 'arrow') {
      const a = frac(el.x1, el.y1)
      const b = frac(el.x2, el.y2)
      const from = at(el.x1, el.y1)
      const to = at(el.x2, el.y2)
      const pt = (x: number, y: number): Frame => ({ x, y, w: 0, h: 0 })
      const link = from || to ? ` from ${ref(from, pt(el.x1, el.y1)) || 'nothing'} to ${ref(to, pt(el.x2, el.y2)) || 'nothing'}` : ''
      lines.push(`${n} ${el.type}, ${colorName(el)},${link} (${pct(a.x)} %, ${pct(a.y)} % → ${pct(b.x)} %, ${pct(b.y)} %)`)
    } else {
      const labels = shown.filter((x) => x.el.type === 'text' && around(bounds(x.el), x.el.id)?.id === el.id).map((x) => quote((x.el as { text: string }).text))
      const inside = around(bounds(el), el.id)
      lines.push(`${n} ${el.type === 'rect' ? 'rectangle' : 'ellipse'}, ${colorName(el)}, ${place(r)}${labels.length ? `, labeled ${labels.join(' ')}` : ''}${inside ? `, inside ${ref(inside, bounds(el))}` : ''}`)
    }
  }
  return lines
}

const dirName = (z: Zone) => (z.split?.dir === 'cols' ? 'columns (left to right)' : 'rows (top to bottom)')

/** A layout as a tree: each part with its size in % of the zone it splits, and its name. */
function layoutLines(el: Layout, n: string, r: Frame): string[] {
  const root = el.root
  const lines = [`${n} layout${root.name ? ` ${quote(root.name)}` : ''}, ${colorName(el)}, ${place(r)}${root.split ? `, split in ${dirName(root)}:` : ', not split'}`]
  const walk = (z: Zone, depth: number) => {
    const s = z.split
    if (!s) return
    s.children.forEach((c, i) => {
      lines.push(`${'  '.repeat(depth + 1)}- ${pct(s.sizes[i])} %${c.name ? ` ${quote(c.name)}` : ''}${c.split ? `, split in ${dirName(c)}:` : ''}`)
      walk(c, depth + 1)
    })
  }
  walk(root, 0)
  return lines
}

/** Does a point touch the box of an element (an arrow end on a shape)? */
function hitBox(el: Element, x: number, y: number): boolean {
  const b = bounds(el)
  return x >= b.x - 12 && x <= b.x + b.w + 12 && y >= b.y - 12 && y <= b.y + b.h + 12
}

/** Text read by the model (always English, like the rest of what it reads). */
export function describe(doc: DoodleDoc, name: string): string {
  const fr = doc.frame
  const preset = doc.preset === 'free' ? 'free ratio' : doc.preset === 'image' ? 'cropped from the image' : presets.find((p) => p.id === doc.preset)?.label
  const seen = doc.elements.map((el) => ({ el, r: inFrame(bounds(el), fr) })).filter((x): x is { el: Element; r: Frame } => !!x.r)
  const lines = [
    `Doodle "${name}" drawn by the user. Frame ${Math.round(fr.w)}×${Math.round(fr.h)} (${preset}); positions in % of the frame from its top left corner.`,
    ...(doc.background ? ['The drawing annotates an image given by the user (a screenshot or a picture), shown under it.'] : []),
    ...items(seen, fr),
    ...strokes(seen.filter((x) => x.el.type === 'pen'), 'Free pen strokes (hand drawn, shapes approximate)'),
    ...strokes(seen.filter((x) => x.el.type === 'marker'), 'Highlighter strokes (emphasis)'),
  ]
  if (!seen.length && !doc.background) lines.push('The frame is empty.')
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
