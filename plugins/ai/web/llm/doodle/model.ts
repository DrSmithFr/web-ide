// Document of a doodle: elements in world coordinates, the frame sent to the model, and the
// geometry of the tools (pressure, erasing, hit test, moves). Kept as plain JSON: it is
// stored in the message.

export type PenColor = 'ink' | 'red' | 'blue' | 'green'
export type FluoColor = 'yellow' | 'lime' | 'pink' | 'cyan'

/** A free stroke; pts holds x, y and the width at each point. */
export interface Stroke {
  id: string
  type: 'pen' | 'marker'
  color: PenColor | FluoColor
  pts: number[]
}

/** An end of a line or an arrow tied to an element, at a place of its box (fractions). */
export interface Bind {
  id: string
  fx: number
  fy: number
}

/**
 * Rectangle and ellipse fill the box of the two points; line and arrow go from 1 to 2, their
 * ends possibly tied to elements (they follow them).
 */
export interface Shape {
  id: string
  type: 'rect' | 'ellipse' | 'line' | 'arrow'
  color: PenColor
  width: number
  x1: number
  y1: number
  x2: number
  y2: number
  from?: Bind
  to?: Bind
  /** Rectangle and ellipse: a light tint of their color inside. */
  fill?: boolean
}

export type TextSize = 's' | 'm' | 'l'

/** Text from its top left corner, lines separated by \n. */
export interface Text {
  id: string
  type: 'text'
  color: PenColor
  size: TextSize
  x: number
  y: number
  text: string
}

/** A zone of a layout, split into rows or columns (sizes are fractions summing to 1), or not. */
export interface Zone {
  name?: string
  split?: { dir: 'rows' | 'cols'; sizes: number[]; children: Zone[] }
}

/** A rectangle split recursively: the structure of a page or a screen. */
export interface Layout {
  id: string
  type: 'layout'
  color: PenColor
  x: number
  y: number
  w: number
  h: number
  root: Zone
}

export type Element = Stroke | Shape | Text | Layout

export interface Frame {
  x: number
  y: number
  w: number
  h: number
}

export type Preset = '16:9' | 'mobile' | 'square' | 'free' | 'image'

/** Image under the drawing (a screenshot to annotate), in world coordinates. */
export interface Background extends Frame {
  src: string
}

export interface DoodleDoc {
  v: 1
  frame: Frame
  preset: Preset
  elements: Element[]
  background?: Background
}

export const presets: { id: Preset; label: string; w: number; h: number }[] = [
  { id: '16:9', label: '16:9', w: 1280, h: 720 },
  { id: 'mobile', label: 'Mobile', w: 390, h: 844 },
  { id: 'square', label: 'Square', w: 800, h: 800 },
  { id: 'free', label: 'Free', w: 1280, h: 720 },
  { id: 'image', label: 'Image', w: 0, h: 0 },
]

/** Colors per theme; the export always uses the light ones. */
export const penColors: Record<PenColor, { light: string; dark: string; name: string }> = {
  ink: { light: '#1f2328', dark: '#e6e8eb', name: 'black' },
  red: { light: '#d1242f', dark: '#ff7b72', name: 'red' },
  blue: { light: '#0969da', dark: '#58a6ff', name: 'blue' },
  green: { light: '#1a7f37', dark: '#56d364', name: 'green' },
}

export const fluoColors: Record<FluoColor, { light: string; dark: string; name: string }> = {
  yellow: { light: '#ffe600', dark: '#e3cf00', name: 'yellow' },
  lime: { light: '#6dff4a', dark: '#4fd33a', name: 'green' },
  pink: { light: '#ff5ccf', dark: '#e04bb4', name: 'pink' },
  cyan: { light: '#4ddcff', dark: '#33b4d6', name: 'cyan' },
}

export const MARKER_OPACITY = 0.45
export const FILL_OPACITY = 0.2
export const PEN_SIZES = { thin: 2, normal: 4 } as const
export const MARKER_SIZE = 18
export const TEXT_SIZES: Record<TextSize, number> = { s: 16, m: 24, l: 36 }
export const LINE_HEIGHT = 1.25
export const FONT = 'system-ui, -apple-system, "Segoe UI", Roboto, Helvetica, Arial, sans-serif'

export const isStroke = (el: Element): el is Stroke => el.type === 'pen' || el.type === 'marker'
export const isShape = (el: Element): el is Shape => el.type === 'rect' || el.type === 'ellipse' || el.type === 'line' || el.type === 'arrow'

export function colorOf(el: Element, dark: boolean): string {
  const c = el.type === 'marker' ? fluoColors[el.color as FluoColor] : penColors[el.color as PenColor]
  return (c ?? penColors.ink)[dark ? 'dark' : 'light']
}

export function colorName(el: Element): string {
  return (el.type === 'marker' ? fluoColors[el.color as FluoColor] : penColors[el.color as PenColor])?.name ?? 'black'
}

export function newDoc(background?: { src: string; w: number; h: number }): DoodleDoc {
  if (background) return { v: 1, frame: { x: 0, y: 0, w: background.w, h: background.h }, preset: 'image', elements: [], background: { ...background, x: 0, y: 0 } }
  return { v: 1, frame: { x: 0, y: 0, w: 1280, h: 720 }, preset: '16:9', elements: [] }
}

let seq = 0
export const newId = () => `${Date.now().toString(36)}${(seq++).toString(36)}`

/** Width of the pen at a point: the stylus pressure (0..1) around the chosen size. */
export function pressureWidth(size: number, pressure: number | undefined): number {
  if (pressure === undefined) return size
  return Math.max(0.6, size * (0.35 + 1.3 * Math.min(1, Math.max(0, pressure))))
}

const round = (v: number) => Math.round(v * 10) / 10

/** Adds a point to a stroke being drawn, filling long gaps so that the eraser can cut anywhere. */
export function addPoint(pts: number[], x: number, y: number, w: number, minGap: number) {
  const n = pts.length
  if (n) {
    const px = pts[n - 3]
    const py = pts[n - 2]
    const d = Math.hypot(x - px, y - py)
    if (d < minGap) return
    const steps = Math.floor(d / 4)
    for (let i = 1; i < steps; i++) {
      const f = i / steps
      pts.push(round(px + (x - px) * f), round(py + (y - py) * f), round(w))
    }
  }
  pts.push(round(x), round(y), round(w))
}

// ---------- text metrics ----------

let ctx: CanvasRenderingContext2D | null = null

export function textLines(el: Text): string[] {
  return el.text.split('\n')
}

/** Size of a text block (measured with the font of the canvas and the export). */
export function textBox(el: Text): { w: number; h: number } {
  const fs = TEXT_SIZES[el.size]
  ctx ??= document.createElement('canvas').getContext('2d')
  let w = fs * 0.6
  if (ctx) {
    ctx.font = `${fs}px ${FONT}`
    for (const l of textLines(el)) w = Math.max(w, ctx.measureText(l).width)
  }
  return { w, h: textLines(el).length * fs * LINE_HEIGHT }
}

// ---------- geometry ----------

export function bounds(el: Element): Frame {
  if (el.type === 'layout') return { x: el.x, y: el.y, w: el.w, h: el.h }
  if (el.type === 'text') {
    const b = textBox(el)
    return { x: el.x, y: el.y, w: b.w, h: b.h }
  }
  if (isShape(el)) {
    const r = el.width / 2
    const x = Math.min(el.x1, el.x2)
    const y = Math.min(el.y1, el.y2)
    return { x: x - r, y: y - r, w: Math.abs(el.x2 - el.x1) + 2 * r, h: Math.abs(el.y2 - el.y1) + 2 * r }
  }
  let x0 = Infinity
  let y0 = Infinity
  let x1 = -Infinity
  let y1 = -Infinity
  for (let i = 0; i < el.pts.length; i += 3) {
    const r = el.pts[i + 2] / 2
    x0 = Math.min(x0, el.pts[i] - r)
    y0 = Math.min(y0, el.pts[i + 1] - r)
    x1 = Math.max(x1, el.pts[i] + r)
    y1 = Math.max(y1, el.pts[i + 1] + r)
  }
  return { x: x0, y: y0, w: x1 - x0, h: y1 - y0 }
}

export function union(boxes: Frame[]): Frame | null {
  if (!boxes.length) return null
  const x0 = Math.min(...boxes.map((b) => b.x))
  const y0 = Math.min(...boxes.map((b) => b.y))
  const x1 = Math.max(...boxes.map((b) => b.x + b.w))
  const y1 = Math.max(...boxes.map((b) => b.y + b.h))
  return { x: x0, y: y0, w: x1 - x0, h: y1 - y0 }
}

export const contains = (outer: Frame, inner: Frame) => inner.x >= outer.x && inner.y >= outer.y && inner.x + inner.w <= outer.x + outer.w && inner.y + inner.h <= outer.y + outer.h

function segDist(px: number, py: number, ax: number, ay: number, bx: number, by: number): number {
  const dx = bx - ax
  const dy = by - ay
  const l = dx * dx + dy * dy
  const t = l ? Math.max(0, Math.min(1, ((px - ax) * dx + (py - ay) * dy) / l)) : 0
  return Math.hypot(px - ax - t * dx, py - ay - t * dy)
}

/** Is the point within r of the element? Closed shapes and texts count their inside. */
export function hit(el: Element, x: number, y: number, r: number): boolean {
  if (el.type === 'text' || el.type === 'layout') {
    const b = bounds(el)
    return x >= b.x - r && x <= b.x + b.w + r && y >= b.y - r && y <= b.y + b.h + r
  }
  if (isShape(el)) {
    const tol = r + el.width / 2
    if (el.type === 'line' || el.type === 'arrow') return segDist(x, y, el.x1, el.y1, el.x2, el.y2) <= tol
    const x0 = Math.min(el.x1, el.x2) - tol
    const y0 = Math.min(el.y1, el.y2) - tol
    const x1 = Math.max(el.x1, el.x2) + tol
    const y1 = Math.max(el.y1, el.y2) + tol
    if (el.type === 'rect') return x >= x0 && x <= x1 && y >= y0 && y <= y1
    const rx = (x1 - x0) / 2 || 1
    const ry = (y1 - y0) / 2 || 1
    return ((x - x0 - rx) / rx) ** 2 + ((y - y0 - ry) / ry) ** 2 <= 1
  }
  const p = el.pts
  if (p.length === 3) return Math.hypot(p[0] - x, p[1] - y) <= r + p[2] / 2
  for (let i = 0; i + 3 < p.length; i += 3) {
    if (segDist(x, y, p[i], p[i + 1], p[i + 3], p[i + 4]) <= r + Math.max(p[i + 2], p[i + 5]) / 2) return true
  }
  return false
}

/**
 * Eraser: the pixel eraser removes the points of a stroke under the circle (the stroke splits
 * into what remains); shapes and texts, and everything with the object eraser, go whole.
 * Returns null when the element is not touched.
 */
export function erase(el: Element, x: number, y: number, r: number, pixel: boolean): Element[] | null {
  if (!pixel || !isStroke(el)) return hit(el, x, y, r) ? [] : null
  const p = el.pts
  const pieces: number[][] = []
  let cur: number[] = []
  let touched = false
  for (let i = 0; i < p.length; i += 3) {
    if (Math.hypot(p[i] - x, p[i + 1] - y) <= r + p[i + 2] / 2) {
      touched = true
      if (cur.length) pieces.push(cur)
      cur = []
    } else cur.push(p[i], p[i + 1], p[i + 2])
  }
  if (!touched) return null
  if (cur.length) pieces.push(cur)
  return pieces.filter((pts) => pts.length >= 6).map((pts, i) => ({ ...el, id: i ? newId() : el.id, pts }))
}

/** The element with every point moved by f. */
export function mapPoints(el: Element, f: (x: number, y: number) => [number, number]): Element {
  if (el.type === 'layout') {
    const [x1, y1] = f(el.x, el.y)
    const [x2, y2] = f(el.x + el.w, el.y + el.h)
    return { ...el, x: round(Math.min(x1, x2)), y: round(Math.min(y1, y2)), w: round(Math.abs(x2 - x1)), h: round(Math.abs(y2 - y1)) }
  }
  if (el.type === 'text') {
    const [x, y] = f(el.x, el.y)
    return { ...el, x: round(x), y: round(y) }
  }
  if (isShape(el)) {
    const [x1, y1] = f(el.x1, el.y1)
    const [x2, y2] = f(el.x2, el.y2)
    return { ...el, x1: round(x1), y1: round(y1), x2: round(x2), y2: round(y2) }
  }
  const pts = el.pts.slice()
  for (let i = 0; i < pts.length; i += 3) {
    const [x, y] = f(pts[i], pts[i + 1])
    pts[i] = round(x)
    pts[i + 1] = round(y)
  }
  return { ...el, pts }
}

export const translate = (el: Element, dx: number, dy: number) => mapPoints(el, (x, y) => [x + dx, y + dy])

/** Moves an element from one box to another (resizing a selection). */
export function rescale(el: Element, from: Frame, to: Frame): Element {
  const sx = from.w ? to.w / from.w : 1
  const sy = from.h ? to.h / from.h : 1
  return mapPoints(el, (x, y) => [to.x + (x - from.x) * sx, to.y + (y - from.y) * sy])
}

// ---------- layouts ----------

export interface ZoneBox {
  path: number[]
  zone: Zone
  box: Frame
}

/** Every zone of a layout with its box, parents before their children. */
export function zones(el: Layout): ZoneBox[] {
  const out: ZoneBox[] = []
  const walk = (zone: Zone, box: Frame, path: number[]) => {
    out.push({ path, zone, box })
    const s = zone.split
    if (!s) return
    let at = 0
    s.children.forEach((child, i) => {
      const f = s.sizes[i]
      const b = s.dir === 'cols' ? { x: box.x + box.w * at, y: box.y, w: box.w * f, h: box.h } : { x: box.x, y: box.y + box.h * at, w: box.w, h: box.h * f }
      at += f
      walk(child, b, [...path, i])
    })
  }
  walk(el.root, { x: el.x, y: el.y, w: el.w, h: el.h }, [])
  return out
}

export interface Divider {
  /** Path of the split zone, and the divider after its child i. */
  path: number[]
  i: number
  dir: 'rows' | 'cols'
  box: Frame
  x1: number
  y1: number
  x2: number
  y2: number
}

export function dividers(el: Layout): Divider[] {
  const out: Divider[] = []
  for (const { path, zone, box } of zones(el)) {
    const s = zone.split
    if (!s) continue
    let at = 0
    for (let i = 0; i < s.sizes.length - 1; i++) {
      at += s.sizes[i]
      if (s.dir === 'cols') out.push({ path, i, dir: s.dir, box, x1: box.x + box.w * at, y1: box.y, x2: box.x + box.w * at, y2: box.y + box.h })
      else out.push({ path, i, dir: s.dir, box, x1: box.x, y1: box.y + box.h * at, x2: box.x + box.w, y2: box.y + box.h * at })
    }
  }
  return out
}

/** The deepest zone under a point. */
export function zoneAt(el: Layout, x: number, y: number): ZoneBox | null {
  let found: ZoneBox | null = null
  for (const z of zones(el)) if (x >= z.box.x && x <= z.box.x + z.box.w && y >= z.box.y && y <= z.box.y + z.box.h) found = z
  return found
}

/** The layout with the zone at path replaced. */
export function updateZone(el: Layout, path: number[], f: (z: Zone) => Zone): Layout {
  const go = (z: Zone, depth: number): Zone => {
    if (depth === path.length) return f(z)
    const s = z.split!
    return { ...z, split: { ...s, children: s.children.map((c, i) => (i === path[depth] ? go(c, depth + 1) : c)) } }
  }
  return { ...el, root: go(el.root, 0) }
}

export const zoneAtPath = (el: Layout, path: number[]): Zone | undefined => path.reduce<Zone | undefined>((z, i) => z?.split?.children[i], el.root)

/**
 * Removes a part of a split zone: its neighbor (the previous one, else the next) takes its
 * place; a zone left with a single part takes the content of that part.
 */
export function removeZone(el: Layout, path: number[]): Layout {
  const i = path[path.length - 1]
  return updateZone(el, path.slice(0, -1), (z) => {
    const s = z.split!
    const to = i > 0 ? i - 1 : 1
    const sizes = s.sizes.map((v, j) => (j === to ? v + s.sizes[i] : v)).filter((_, j) => j !== i)
    const children = s.children.filter((_, j) => j !== i)
    if (children.length === 1) return { name: z.name ?? children[0].name, split: children[0].split }
    return { ...z, split: { ...s, sizes, children } }
  })
}

const even = (n: number) => Array.from({ length: n }, () => 1 / n)

export function splitZone(z: Zone, dir: 'rows' | 'cols', n: number): Zone {
  return { ...z, split: { dir, sizes: even(n), children: Array.from({ length: n }, () => ({})) } }
}

export function gridZone(z: Zone, rows: number, cols: number): Zone {
  return { ...z, split: { dir: 'rows', sizes: even(rows), children: Array.from({ length: rows }, () => splitZone({}, 'cols', cols)) } }
}

/** North, south, west, east and center. */
export function borderZone(z: Zone): Zone {
  const middle: Zone = { split: { dir: 'cols', sizes: [0.2, 0.6, 0.2], children: [{ name: 'west' }, { name: 'center' }, { name: 'east' }] } }
  return { ...z, split: { dir: 'rows', sizes: [0.15, 0.7, 0.15], children: [{ name: 'north' }, middle, { name: 'south' }] } }
}

/** Plain copy (a document read from the conversation store is a proxy). */
export const cloneDoc = (d: DoodleDoc): DoodleDoc => JSON.parse(JSON.stringify(d))

// ---------- ties of arrows ----------

export const isLink = (el: Element): el is Shape => el.type === 'line' || el.type === 'arrow'

/** Can an end of a line or an arrow be tied to this element? */
export const bindable = (el: Element) => !isLink(el) && !isStroke(el)

/** The tie of a point to an element: where it falls in its box, kept inside the box. */
export function bindTo(el: Element, x: number, y: number): Bind {
  const b = bounds(el)
  const clamp = (v: number) => Math.round(Math.min(1, Math.max(0, v)) * 1000) / 1000
  return { id: el.id, fx: b.w ? clamp((x - b.x) / b.w) : 0.5, fy: b.h ? clamp((y - b.y) / b.h) : 0.5 }
}

/** Moves the tied ends to their elements; a tie to an element gone is dropped. */
export function follow(d: DoodleDoc): DoodleDoc {
  const byId = new Map(d.elements.map((e) => [e.id, e]))
  let changed = false
  const elements = d.elements.map((el) => {
    if (!isShape(el) || !isLink(el) || (!el.from && !el.to)) return el
    let n: Shape = el
    for (const end of ['from', 'to'] as const) {
      const tie = n[end]
      if (!tie) continue
      const target = byId.get(tie.id)
      if (!target) {
        n = { ...n, [end]: undefined }
        continue
      }
      const b = bounds(target)
      const x = round(b.x + tie.fx * b.w)
      const y = round(b.y + tie.fy * b.h)
      if (end === 'from' && (x !== n.x1 || y !== n.y1)) n = { ...n, x1: x, y1: y }
      if (end === 'to' && (x !== n.x2 || y !== n.y2)) n = { ...n, x2: x, y2: y }
    }
    if (n !== el) changed = true
    return n
  })
  return changed ? { ...d, elements } : d
}

/** Lines and arrows moved without the element an end is tied to come loose from it. */
export function loosen(d: DoodleDoc, moved: Set<string>): DoodleDoc {
  let changed = false
  const elements = d.elements.map((el) => {
    if (!moved.has(el.id) || !isShape(el) || !isLink(el)) return el
    const from = el.from && moved.has(el.from.id) ? el.from : undefined
    const to = el.to && moved.has(el.to.id) ? el.to : undefined
    if (from === el.from && to === el.to) return el
    changed = true
    return { ...el, from, to }
  })
  return changed ? { ...d, elements } : d
}

/** Copies keep their ties among themselves only. */
export function retie(copies: Element[], ids: Map<string, string>): Element[] {
  return copies.map((el) => {
    if (!isShape(el) || !isLink(el)) return el
    const map = (b?: Bind) => (b && ids.has(b.id) ? { ...b, id: ids.get(b.id)! } : undefined)
    return { ...el, from: map(el.from), to: map(el.to) }
  })
}

export const isEmpty = (d: DoodleDoc) => d.elements.length === 0 && !d.background
