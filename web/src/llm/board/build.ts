// board_draw: the page described by the model, built as a doodle document. Coordinates are
// pixels from the top left corner of the frame; lines and arrows tie to elements by the ids
// the model gives. An invalid element throws an error naming it, which the model reads.
import {
  bounds,
  bindTo,
  cloneDoc,
  fluoColors,
  follow,
  MARKER_SIZE,
  PEN_SIZES,
  penColors,
  presets,
  TEXT_SIZES,
  textBox,
  newId,
  type DoodleDoc,
  type Element,
  type FluoColor,
  type PenColor,
  type Shape,
  type Text,
  type TextSize,
  type Zone,
} from '../doodle/model'

export interface DrawArgs {
  title: string
  preset?: string
  size?: { w?: number; h?: number }
  /** Document of the page copied (the pod sends it with its number). */
  fromDoc?: DoodleDoc
  /** A blank page on a background image (its frame). */
  base?: DoodleDoc
  elements: Record<string, any>[]
}

export interface Built {
  doc: DoodleDoc
  /** Elements partly outside the frame (said to the model). */
  outside: string[]
}

const num = (v: unknown) => typeof v === 'number' && Number.isFinite(v)

function frameOf(a: DrawArgs): DoodleDoc {
  if (a.fromDoc) return cloneDoc(a.fromDoc)
  if (a.base) return a.base
  if (a.size && (a.size.w || a.size.h)) {
    const w = Number(a.size.w)
    const h = Number(a.size.h)
    if (!(w >= 50 && h >= 50 && w <= 4000 && h <= 4000)) throw new Error('size: w and h between 50 and 4000')
    return { v: 1, frame: { x: 0, y: 0, w, h }, preset: 'free', elements: [] }
  }
  const p = presets.find((x) => x.id === (a.preset || '16:9') && x.w > 0 && x.id !== 'free')
  if (!p) throw new Error(`preset: 16:9, mobile or square (not "${a.preset}")`)
  return { v: 1, frame: { x: 0, y: 0, w: p.w, h: p.h }, preset: p.id, elements: [] }
}

/** Builds the page: the frame (blank or a copy), then the elements on top. */
export function buildPage(a: DrawArgs): Built {
  if (!Array.isArray(a.elements)) throw new Error('elements must be a list')
  const doc = frameOf(a)
  const { x: ox, y: oy } = doc.frame
  const ids = new Map<string, Element>()
  const out: Element[] = []
  const links: { el: Shape; from: unknown; to: unknown; where: string }[] = []
  a.elements.forEach((e, i) => {
    const where = `element ${i + 1} (${e?.type ?? '?'})`
    const fail = (msg: string): never => {
      throw new Error(`${where}: ${msg}`)
    }
    const pen = (): PenColor => {
      const c = (e.color ?? 'ink') as PenColor
      return c in penColors ? c : fail(`color "${e.color}": ink, red, blue or green`)
    }
    const box = () => {
      for (const k of ['x', 'y', 'w', 'h']) if (!num(e[k])) fail(`${k} must be a number`)
      if (e.w <= 0 || e.h <= 0) fail('w and h must be positive')
      return { x: ox + e.x, y: oy + e.y, w: e.w, h: e.h }
    }
    const add = (el: Element) => {
      out.push(el)
      if (e.id !== undefined) {
        const id = String(e.id)
        if (ids.has(id)) fail(`id "${id}" is used twice`)
        ids.set(id, el)
      }
    }
    switch (e?.type) {
      case 'rect':
      case 'ellipse': {
        const b = box()
        add({ id: newId(), type: e.type, color: pen(), width: PEN_SIZES.normal, x1: b.x, y1: b.y, x2: b.x + b.w, y2: b.y + b.h, fill: e.fill ? true : undefined })
        if (e.label) {
          // A text inside a shape is read as its label (export.ts describe).
          const t: Text = { id: newId(), type: 'text', color: pen(), size: 'm', x: 0, y: 0, text: String(e.label) }
          const tb = textBox(t)
          out.push({ ...t, x: b.x + (b.w - tb.w) / 2, y: b.y + (b.h - tb.h) / 2 })
        }
        return
      }
      case 'line':
      case 'arrow': {
        const el: Shape = { id: newId(), type: e.type, color: pen(), width: PEN_SIZES.normal, x1: 0, y1: 0, x2: 0, y2: 0 }
        add(el)
        links.push({ el, from: e.from, to: e.to, where })
        return
      }
      case 'text': {
        if (!num(e.x) || !num(e.y)) fail('x and y must be numbers')
        if (!String(e.text ?? '').trim()) fail('text is empty')
        const size = (e.size ?? 'm') as TextSize
        if (!(size in TEXT_SIZES)) fail('size: s, m or l')
        add({ id: newId(), type: 'text', color: pen(), size, x: ox + e.x, y: oy + e.y, text: String(e.text) })
        return
      }
      case 'layout': {
        const b = box()
        add({ id: newId(), type: 'layout', color: pen(), ...b, root: zone(e.root ?? {}, 'root', fail) })
        return
      }
      case 'stroke': {
        if (!Array.isArray(e.points) || e.points.length < 2) fail('points: at least 2 [x, y]')
        const marker = !!e.marker
        let color: PenColor | FluoColor
        if (marker) {
          color = (e.color ?? 'yellow') as FluoColor
          if (!(color in fluoColors)) fail(`marker color "${e.color}": yellow, lime, pink or cyan`)
        } else color = pen()
        const w = marker ? MARKER_SIZE : PEN_SIZES.normal
        const pts: number[] = []
        e.points.forEach((p: unknown, k: number) => {
          if (!Array.isArray(p) || !num(p[0]) || !num(p[1])) fail(`point ${k + 1} must be [x, y]`)
          const q = p as number[]
          pts.push(ox + q[0], oy + q[1], w)
        })
        add({ id: newId(), type: marker ? 'marker' : 'pen', color, pts })
        return
      }
      default:
        fail('type: rect, ellipse, line, arrow, text, layout or stroke')
    }
  })
  // Lines and arrows: a free end is a point, a tied end goes to the middle of the side of its
  // element facing the other end, then follows the element.
  const centre = (el: Element) => {
    const b = bounds(el)
    return [b.x + b.w / 2, b.y + b.h / 2]
  }
  for (const l of links) {
    const end = (v: unknown, name: string): { el?: Element; p: number[] } => {
      if (Array.isArray(v) && num(v[0]) && num(v[1])) return { p: [ox + v[0], oy + v[1]] }
      if (typeof v === 'string' || typeof v === 'number') {
        const el = ids.get(String(v))
        if (!el) throw new Error(`${l.where}: ${name} "${v}" is no element id (${[...ids.keys()].map((k) => `"${k}"`).join(', ') || 'no id given'})`)
        return { el, p: centre(el) }
      }
      throw new Error(`${l.where}: ${name} must be an element id or [x, y]`)
    }
    const a1 = end(l.from, 'from')
    const a2 = end(l.to, 'to')
    const tie = (e: { el?: Element; p: number[] }, other: number[]) => {
      if (!e.el) return { p: e.p }
      const b = bounds(e.el)
      const sides = [
        [b.x + b.w / 2, b.y],
        [b.x + b.w, b.y + b.h / 2],
        [b.x + b.w / 2, b.y + b.h],
        [b.x, b.y + b.h / 2],
      ]
      const s = sides.reduce((best, c) => (Math.hypot(c[0] - other[0], c[1] - other[1]) < Math.hypot(best[0] - other[0], best[1] - other[1]) ? c : best))
      return { p: s, bind: bindTo(e.el, s[0], s[1]) }
    }
    const t1 = tie(a1, a2.p)
    const t2 = tie(a2, a1.p)
    Object.assign(l.el, { x1: t1.p[0], y1: t1.p[1], x2: t2.p[0], y2: t2.p[1], from: t1.bind, to: t2.bind })
  }
  const built = follow({ ...doc, elements: [...doc.elements, ...out] })
  const fr = built.frame
  const outside = out
    .filter((el) => {
      const b = bounds(el)
      return b.x < fr.x - 1 || b.y < fr.y - 1 || b.x + b.w > fr.x + fr.w + 1 || b.y + b.h > fr.y + fr.h + 1
    })
    .map((el) => `${el.type}${'text' in el ? ` "${(el as Text).text}"` : ''}`)
  return { doc: built, outside }
}

/** A zone of a layout: sizes normalised to fractions summing to 1. */
function zone(z: any, where: string, fail: (msg: string) => never): Zone {
  if (typeof z !== 'object' || z === null) fail(`${where}: a zone is an object`)
  const out: Zone = {}
  if (z.name) out.name = String(z.name)
  if (z.split) {
    const s = z.split
    if (s.dir !== 'rows' && s.dir !== 'cols') fail(`${where}: split.dir is rows or cols`)
    if (!Array.isArray(s.children) || s.children.length < 2) fail(`${where}: split.children needs 2 zones or more`)
    let sizes: number[] = Array.isArray(s.sizes) ? s.sizes.map(Number) : s.children.map(() => 1)
    if (sizes.length !== s.children.length || sizes.some((v) => !(v > 0))) fail(`${where}: split.sizes needs one positive size per child`)
    const total = sizes.reduce((x, y) => x + y, 0)
    sizes = sizes.map((v) => v / total)
    out.split = { dir: s.dir, sizes, children: s.children.map((c: any, i: number) => zone(c, `${where}.children[${i}]`, fail)) }
  }
  return out
}
