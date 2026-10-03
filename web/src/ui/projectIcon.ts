// Project icons: a glyph or 1 to 3 characters on a shape filled with a colour or a two-colour
// gradient. The page draws the SVG; the pod keeps it in <project>/.ide/icon.svg (with the
// description in icon.json). Shown on the home page, in the menu bar and as the favicon of
// every window of the project, so that browser tabs tell the projects apart.
import { createEffect, createSignal, onCleanup } from 'solid-js'
import { on, request } from '../pod/rpc'

export type IconShape = 'circle' | 'rounded' | 'square' | 'hexagon' | 'diamond'

export interface IconSpec {
  kind: 'glyph' | 'text'
  glyph?: string
  text?: string
  shape: IconShape
  color: string
  /** Second colour: the background is a gradient from color to color2. */
  color2?: string
  /** Direction of the gradient in degrees: 0 to the right, 90 downwards. */
  angle?: number
  fg: string
}

export const shapes: IconShape[] = ['circle', 'rounded', 'square', 'hexagon', 'diamond']
export const palette = ['#ef4444', '#f97316', '#f59e0b', '#84cc16', '#22c55e', '#14b8a6', '#06b6d4', '#3b82f6', '#6366f1', '#8b5cf6', '#d946ef', '#ec4899', '#64748b', '#1e293b', '#ffffff']
export const angles = [0, 45, 90, 135, 180, 225, 270, 315]

const outline: Record<IconShape, string> = {
  circle: '<circle cx="32" cy="32" r="32"',
  rounded: '<rect width="64" height="64" rx="15"',
  square: '<rect width="64" height="64" rx="3"',
  hexagon: '<polygon points="32,0 59.7,16 59.7,48 32,64 4.3,48 4.3,16"',
  diamond: '<polygon points="32,0 64,32 32,64 0,32"',
}
// Side of the square the glyph or text fits in, per shape.
const box: Record<IconShape, number> = { circle: 38, rounded: 42, square: 44, hexagon: 36, diamond: 28 }

const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')

/** The SVG of an icon; glyph is the inner markup of the glyph (24×24 Lucide icon). */
export function renderIcon(spec: IconSpec, glyph = ''): string {
  const b = box[spec.shape] ?? 40
  let defs = ''
  let fill = esc(spec.color)
  if (spec.color2) {
    const a = ((spec.angle ?? 90) * Math.PI) / 180
    const c = (v: number) => (50 + 50 * v).toFixed(1) + '%'
    defs = `<defs><linearGradient id="g" x1="${c(-Math.cos(a))}" y1="${c(-Math.sin(a))}" x2="${c(Math.cos(a))}" y2="${c(Math.sin(a))}"><stop offset="0" stop-color="${esc(spec.color)}"/><stop offset="1" stop-color="${esc(spec.color2)}"/></linearGradient></defs>`
    fill = 'url(#g)'
  }
  let content = ''
  if (spec.kind === 'glyph' && glyph) {
    const s = b / 24
    content = `<g transform="translate(${32 - b / 2} ${32 - b / 2}) scale(${s})" fill="none" stroke="${esc(spec.fg)}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${glyph}</g>`
  } else {
    const text = [...(spec.text ?? '')].slice(0, 3).join('')
    const size = b * [0.9, 0.9, 0.66, 0.5][[...text].length]
    content = `<text x="32" y="33" text-anchor="middle" dominant-baseline="central" font-family="system-ui, -apple-system, Segoe UI, sans-serif" font-weight="700" font-size="${size.toFixed(1)}" fill="${esc(spec.fg)}">${esc(text)}</text>`
  }
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">${defs}${outline[spec.shape] ?? outline.rounded} fill="${fill}"/>${content}</svg>`
}

/** The icon of a ticket worktree: the icon of its project with a dot in the corner. */
export function withBadge(svg: string): string {
  const inner = svg.replace('<svg ', '<svg width="64" height="64" ')
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">${inner}<circle cx="52" cy="52" r="12" fill="#f59e0b" stroke="#fff" stroke-width="4"/></svg>`
}

export const iconURL = (svg: string) => 'data:image/svg+xml,' + encodeURIComponent(svg)

function hash(s: string) {
  let h = 2166136261
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619)
  return h >>> 0
}

/** Initials of a name: first letters of the first two words, or its first two letters. */
export function initials(name: string) {
  const words = name.split(/[\s_.\-@/]+|(?<=[a-z])(?=[A-Z])/).filter((w) => /[\p{L}\p{N}]/u.test(w))
  const first = (w: string) => [...w].find((ch) => /[\p{L}\p{N}]/u.test(ch)) ?? ''
  const s = words.length >= 2 ? first(words[0]) + first(words[1]) : [...(words[0] ?? '?')].filter((ch) => /[\p{L}\p{N}]/u.test(ch)).slice(0, 2).join('')
  return (s || '?').toUpperCase()
}

/** Icon generated for a project that has none: initials, colour and shape from its id. */
export function defaultSpec(id: string, name: string): IconSpec {
  const h = hash(id)
  return { kind: 'text', text: initials(name), shape: (['rounded', 'circle', 'hexagon'] as IconShape[])[(h >>> 8) % 3], color: palette[h % 12], fg: '#ffffff' }
}

// ---------- icons of the projects, kept up to date ----------

const [icons, setIcons] = createSignal<Record<string, string>>({})
on('projects.iconChanged', (d: { id: string; svg: string }) => setIcons((m) => ({ ...m, [d.id]: d.svg })))

/** The SVG of the icon of a project ("" while unknown). */
export const iconOf = (id: string) => icons()[id] ?? ''

/** Saves an icon in the .ide folder of the project (or of the parent of a worktree). */
export async function saveIcon(id: string, spec: IconSpec, svg: string) {
  await request('projects.icon.save', { id, spec, svg })
}

/** Loads the icons of the listed projects; generates and saves the missing ones. */
export async function loadIcons(list: { id: string; name: string }[]) {
  const got = await request<Record<string, string>>('projects.icons').catch(() => ({}) as Record<string, string>)
  for (const p of list) {
    if (got[p.id]) continue
    const spec = defaultSpec(p.id, p.name)
    got[p.id] = renderIcon(spec)
    // An SSH project not open cannot store it yet: drawn here until its next opening.
    saveIcon(p.id, spec, got[p.id]).catch(() => {})
  }
  setIcons((m) => ({ ...m, ...got }))
}

export interface IconOwner {
  /** The project whose icon is shown: the project, or the parent of a worktree. */
  owner: string
  name: string
  spec: IconSpec | null
}

/** Loads the icon of a project (its parent's for a worktree), generating it when missing. */
export async function loadIcon(id: string): Promise<IconOwner> {
  const r = await request<{ owner: string; name: string; spec: IconSpec | null; svg: string }>('projects.icon', { id })
  let { spec, svg } = r
  if (!svg) {
    spec = defaultSpec(r.owner, r.name)
    svg = renderIcon(spec)
    saveIcon(id, spec, svg).catch(() => {})
  }
  setIcons((m) => ({ ...m, [r.owner]: svg }))
  return { owner: r.owner, name: r.name, spec }
}

let appIcon: string | null = null

/** Shows an icon as the favicon of the window; no icon: the icon of the application. */
export function setFavicon(svg: string | null) {
  let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (!link) {
    link = document.createElement('link')
    link.rel = 'icon'
    document.head.append(link)
  }
  appIcon ??= link.href
  link.href = svg ? iconURL(svg) : appIcon
}

/**
 * Keeps the favicon of a project window on the icon of the project (with a dot for a
 * worktree); returns the owner of the icon (with its name and description).
 */
export function useProjectIcon(project: () => { id: string; parent?: string } | null) {
  const [owner, setOwner] = createSignal<IconOwner | null>(null)
  const load = () => {
    const p = project()
    if (p) loadIcon(p.id).then(setOwner, () => {})
  }
  createEffect(load)
  // The description changed in another window: read it again.
  onCleanup(on('projects.iconChanged', (d: { id: string }) => d.id === owner()?.owner && load()))
  createEffect(() => {
    const svg = iconOf(owner()?.owner ?? '')
    if (svg) setFavicon(project()?.parent ? withBadge(svg) : svg)
  })
  return owner
}
