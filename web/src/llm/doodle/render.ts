// SVG geometry of the elements, shared by the canvas and the export.
import { colorOf, dividers, FONT, isShape, LINE_HEIGHT, MARKER_OPACITY, TEXT_SIZES, textLines, zones, type DoodleDoc, type Element, type Layout, type Shape } from './model'

type Pt = [number, number]

const f = (v: number) => Math.round(v * 10) / 10

/** Smooth path through points (quadratic curves between the middles). */
function smooth(pts: Pt[]): string {
  if (pts.length === 1) return `M${f(pts[0][0])} ${f(pts[0][1])}l0 0`
  let d = `M${f(pts[0][0])} ${f(pts[0][1])}`
  for (let i = 1; i < pts.length - 1; i++) {
    const [x, y] = pts[i]
    const [nx, ny] = pts[i + 1]
    d += `Q${f(x)} ${f(y)} ${f((x + nx) / 2)} ${f((y + ny) / 2)}`
  }
  const last = pts[pts.length - 1]
  return d + `L${f(last[0])} ${f(last[1])}`
}

/** What is drawn: a path (stroked when width is set, else filled) or text lines. */
export type Prim =
  | { kind: 'path'; d: string; width?: number; color: string; opacity: number }
  | { kind: 'text'; x: number; y: number; lines: string[]; size: number; color: string; anchor?: 'middle' }

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

function shapePath(el: Shape): string {
  const { x1, y1, x2, y2 } = el
  if (el.type === 'rect') {
    const x = Math.min(x1, x2)
    const y = Math.min(y1, y2)
    return `M${f(x)} ${f(y)}h${f(Math.abs(x2 - x1))}v${f(Math.abs(y2 - y1))}h${f(-Math.abs(x2 - x1))}Z`
  }
  if (el.type === 'ellipse') {
    const rx = Math.abs(x2 - x1) / 2
    const ry = Math.abs(y2 - y1) / 2
    const cx = Math.min(x1, x2) + rx
    const cy = Math.min(y1, y2)
    return `M${f(cx)} ${f(cy)}A${f(rx)} ${f(ry)} 0 1 1 ${f(cx)} ${f(cy + 2 * ry)}A${f(rx)} ${f(ry)} 0 1 1 ${f(cx)} ${f(cy)}Z`
  }
  let d = `M${f(x1)} ${f(y1)}L${f(x2)} ${f(y2)}`
  if (el.type === 'arrow') {
    const a = Math.atan2(y2 - y1, x2 - x1)
    const len = Math.min(Math.max(12, el.width * 4), Math.hypot(x2 - x1, y2 - y1) * 0.6)
    for (const s of [-1, 1]) d += `M${f(x2)} ${f(y2)}l${f(-len * Math.cos(a + (s * Math.PI) / 7))} ${f(-len * Math.sin(a + (s * Math.PI) / 7))}`
  }
  return d
}

export const ZONE_NAME_SIZE = 18

/** A layout: its outline, its dividers and the names of its zones. */
function layoutPrims(el: Layout, color: string): Prim[] {
  let d = `M${f(el.x)} ${f(el.y)}h${f(el.w)}v${f(el.h)}h${f(-el.w)}Z`
  for (const v of dividers(el)) d += `M${f(v.x1)} ${f(v.y1)}L${f(v.x2)} ${f(v.y2)}`
  const out: Prim[] = [{ kind: 'path', d, width: 2, color, opacity: 1 }]
  for (const { zone, box } of zones(el)) {
    if (!zone.name || zone.split) continue
    out.push({ kind: 'text', x: box.x + box.w / 2, y: box.y + box.h / 2 - (ZONE_NAME_SIZE * LINE_HEIGHT) / 2, lines: [zone.name], size: ZONE_NAME_SIZE, color, anchor: 'middle' })
  }
  return out
}

export function primsOf(el: Element, dark: boolean): Prim[] {
  const color = colorOf(el, dark)
  if (el.type === 'layout') return layoutPrims(el, color)
  return [primOf(el, color)]
}

function primOf(el: Exclude<Element, Layout>, color: string): Prim {
  if (el.type === 'text') return { kind: 'text', x: el.x, y: el.y, lines: textLines(el), size: TEXT_SIZES[el.size], color }
  if (isShape(el)) return { kind: 'path', d: shapePath(el), width: el.width, color, opacity: 1 }
  const p = el.pts
  const opacity = el.type === 'marker' ? MARKER_OPACITY : 1
  let constant = true
  for (let i = 5; i < p.length; i += 3) if (p[i] !== p[2]) constant = false
  if (constant || p.length < 9) {
    const pts: Pt[] = []
    for (let i = 0; i < p.length; i += 3) pts.push([p[i], p[i + 1]])
    return { kind: 'path', d: smooth(pts), width: p[2], color, opacity }
  }
  return { kind: 'path', d: outline(p), color, opacity }
}

/** Layouts at the bottom, then markers (like a highlighter), then everything else. */
export function layered(doc: DoodleDoc): Element[] {
  const rank = (e: Element) => (e.type === 'layout' ? 0 : e.type === 'marker' ? 1 : 2)
  return [0, 1, 2].flatMap((r) => doc.elements.filter((e) => rank(e) === r))
}

/** Baseline of the first line of a text from its top. */
export const baseline = (size: number) => size * 0.95

function esc(s: string) {
  return s.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

function primSVG(p: Prim): string {
  if (p.kind === 'text') {
    const spans = p.lines.map((l, i) => `<tspan x="${p.x}" dy="${i ? p.size * LINE_HEIGHT : 0}">${esc(l) || ' '}</tspan>`).join('')
    return `<text x="${p.x}" y="${f(p.y + baseline(p.size))}"${p.anchor ? ` text-anchor="${p.anchor}"` : ''} font-size="${p.size}" font-family="${esc(FONT)}" fill="${p.color}" xml:space="preserve">${spans}</text>`
  }
  return p.width !== undefined
    ? `<path d="${esc(p.d)}" fill="none" stroke="${p.color}" stroke-width="${p.width}" stroke-linecap="round" stroke-linejoin="round" stroke-opacity="${p.opacity}"/>`
    : `<path d="${esc(p.d)}" fill="${p.color}" fill-opacity="${p.opacity}"/>`
}

/** Standalone SVG of the frame, light colors on white. */
export function toSVG(doc: DoodleDoc, width: number, height: number): string {
  const { x, y, w, h } = doc.frame
  const bg = doc.background
  const image = bg ? `<image href="${esc(bg.src)}" x="${bg.x}" y="${bg.y}" width="${bg.w}" height="${bg.h}" preserveAspectRatio="none"/>` : ''
  const body = layered(doc)
    .flatMap((el) => primsOf(el, false).map(primSVG))
    .join('')
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="${x} ${y} ${w} ${h}"><rect x="${x}" y="${y}" width="${w}" height="${h}" fill="#fff"/>${image}${body}</svg>`
}
