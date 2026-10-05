// Document of a doodle: elements in world coordinates, the frame sent to the model, and the
// geometry of the tools (pressure, erasing). Kept as plain JSON: it is stored in the message.

export type PenColor = 'ink' | 'red' | 'blue' | 'green'
export type FluoColor = 'yellow' | 'lime' | 'pink' | 'cyan'

/** A free stroke; pts holds x, y and the width at each point. */
export interface Stroke {
  id: string
  type: 'pen' | 'marker'
  color: PenColor | FluoColor
  pts: number[]
}

export type Element = Stroke

export interface Frame {
  x: number
  y: number
  w: number
  h: number
}

export type Preset = '16:9' | 'mobile' | 'square' | 'free'

export interface DoodleDoc {
  v: 1
  frame: Frame
  preset: Preset
  elements: Element[]
}

export const presets: { id: Preset; label: string; w: number; h: number }[] = [
  { id: '16:9', label: '16:9', w: 1280, h: 720 },
  { id: 'mobile', label: 'Mobile', w: 390, h: 844 },
  { id: 'square', label: 'Square', w: 800, h: 800 },
  { id: 'free', label: 'Free', w: 1280, h: 720 },
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
export const PEN_SIZES = { thin: 2, normal: 4 } as const
export const MARKER_SIZE = 18

export function colorOf(el: Element, dark: boolean): string {
  const c = el.type === 'marker' ? fluoColors[el.color as FluoColor] : penColors[el.color as PenColor]
  return (c ?? penColors.ink)[dark ? 'dark' : 'light']
}

export function colorName(el: Element): string {
  return (el.type === 'marker' ? fluoColors[el.color as FluoColor] : penColors[el.color as PenColor])?.name ?? 'black'
}

export function newDoc(preset: Preset = '16:9'): DoodleDoc {
  const p = presets.find((x) => x.id === preset)!
  return { v: 1, frame: { x: 0, y: 0, w: p.w, h: p.h }, preset, elements: [] }
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

export function bounds(el: Element): Frame {
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

function segDist(px: number, py: number, ax: number, ay: number, bx: number, by: number): number {
  const dx = bx - ax
  const dy = by - ay
  const l = dx * dx + dy * dy
  const t = l ? Math.max(0, Math.min(1, ((px - ax) * dx + (py - ay) * dy) / l)) : 0
  return Math.hypot(px - ax - t * dx, py - ay - t * dy)
}

/** Does a circle of the eraser touch the element? */
export function touches(el: Element, x: number, y: number, r: number): boolean {
  const p = el.pts
  if (p.length === 3) return Math.hypot(p[0] - x, p[1] - y) <= r + p[2] / 2
  for (let i = 0; i + 3 < p.length; i += 3) {
    if (segDist(x, y, p[i], p[i + 1], p[i + 3], p[i + 4]) <= r + Math.max(p[i + 2], p[i + 5]) / 2) return true
  }
  return false
}

/** Pixel eraser: removes the points under the circle, the stroke splits into what remains. */
export function erasePoints(el: Element, x: number, y: number, r: number): Element[] | null {
  const p = el.pts
  const pieces: number[][] = []
  let cur: number[] = []
  let hit = false
  for (let i = 0; i < p.length; i += 3) {
    if (Math.hypot(p[i] - x, p[i + 1] - y) <= r + p[i + 2] / 2) {
      hit = true
      if (cur.length) pieces.push(cur)
      cur = []
    } else cur.push(p[i], p[i + 1], p[i + 2])
  }
  if (!hit) return null
  if (cur.length) pieces.push(cur)
  return pieces.filter((pts) => pts.length >= 6).map((pts, i) => ({ ...el, id: i ? newId() : el.id, pts }))
}

export const isEmpty = (d: DoodleDoc) => d.elements.length === 0
