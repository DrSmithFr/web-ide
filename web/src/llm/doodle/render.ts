// SVG geometry of the elements, shared by the canvas and the export.
import { colorOf, MARKER_OPACITY, type DoodleDoc, type Element } from './model'

type Pt = [number, number]

const f = (v: number) => Math.round(v * 10) / 10

/** Smooth path through points (quadratic curves between the middles). */
function smooth(pts: Pt[], close = false): string {
  if (pts.length === 1) return `M${f(pts[0][0])} ${f(pts[0][1])}l0 0`
  let d = `M${f(pts[0][0])} ${f(pts[0][1])}`
  for (let i = 1; i < pts.length - 1; i++) {
    const [x, y] = pts[i]
    const [nx, ny] = pts[i + 1]
    d += `Q${f(x)} ${f(y)} ${f((x + nx) / 2)} ${f((y + ny) / 2)}`
  }
  const last = pts[pts.length - 1]
  return d + `L${f(last[0])} ${f(last[1])}` + (close ? 'Z' : '')
}

export interface Shape {
  d: string
  /** Stroked with this width, else filled (outline of a stroke of varying width). */
  width?: number
  color: string
  opacity: number
}

/** Outline of a stroke whose width changes along the way, with round ends. */
function outline(p: number[]): string {
  const n = p.length / 3
  const left: Pt[] = []
  const right: Pt[] = []
  for (let i = 0; i < n; i++) {
    const a = Math.max(0, i - 1) * 3
    const b = Math.min(n - 1, i + 1) * 3
    let dx = p[b] - p[a]
    let dy = p[b + 1] - p[a + 1]
    const l = Math.hypot(dx, dy) || 1
    dx /= l
    dy /= l
    const r = p[i * 3 + 2] / 2
    left.push([p[i * 3] - dy * r, p[i * 3 + 1] + dx * r])
    right.push([p[i * 3] + dy * r, p[i * 3 + 1] - dx * r])
  }
  const re = p[(n - 1) * 3 + 2] / 2
  const rs = p[2] / 2
  const end = right[n - 1]
  const start = left[0]
  return (
    smooth(left) +
    `A${f(re)} ${f(re)} 0 0 0 ${f(end[0])} ${f(end[1])}` +
    smooth(right.reverse()).replace(/^M/, 'L') +
    `A${f(rs)} ${f(rs)} 0 0 0 ${f(start[0])} ${f(start[1])}Z`
  )
}

export function shapeOf(el: Element, dark: boolean): Shape {
  const p = el.pts
  const color = colorOf(el, dark)
  const opacity = el.type === 'marker' ? MARKER_OPACITY : 1
  let constant = true
  for (let i = 5; i < p.length; i += 3) if (p[i] !== p[2]) constant = false
  if (constant || p.length < 9) {
    const pts: Pt[] = []
    for (let i = 0; i < p.length; i += 3) pts.push([p[i], p[i + 1]])
    return { d: smooth(pts), width: p[2], color, opacity }
  }
  return { d: outline(p), color, opacity }
}

/** Markers under the pen strokes, like a highlighter. */
export function layered(doc: DoodleDoc): Element[] {
  return [...doc.elements.filter((e) => e.type === 'marker'), ...doc.elements.filter((e) => e.type !== 'marker')]
}

function attr(s: string) {
  return s.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;')
}

/** Standalone SVG of the frame, light colors on white. */
export function toSVG(doc: DoodleDoc, width: number, height: number): string {
  const { x, y, w, h } = doc.frame
  const body = layered(doc)
    .map((el) => {
      const s = shapeOf(el, false)
      return s.width !== undefined
        ? `<path d="${attr(s.d)}" fill="none" stroke="${s.color}" stroke-width="${s.width}" stroke-linecap="round" stroke-linejoin="round" stroke-opacity="${s.opacity}"/>`
        : `<path d="${attr(s.d)}" fill="${s.color}" fill-opacity="${s.opacity}"/>`
    })
    .join('')
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="${x} ${y} ${w} ${h}"><rect x="${x}" y="${y}" width="${w}" height="${h}" fill="#fff"/>${body}</svg>`
}
