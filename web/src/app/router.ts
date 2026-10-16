// Minimal router: /, /project/:id (desk 1), /project/:id/desk/:n, and the detached panels
// /project/:id/editor, /project/:id/tool/:toolId; under the base path of the page.
import { createSignal } from 'solid-js'
import { base, podPath } from './base'

const current = () => location.pathname.slice(base.length) || '/'
const [path, setPath] = createSignal(current())
export { path }

window.addEventListener('popstate', () => setPath(current()))

export function navigate(to: string, replace = false) {
  if (to === current()) return
  if (replace) history.replaceState(null, '', podPath(to))
  else history.pushState(null, '', podPath(to))
  setPath(to)
}

export type Route =
  | { name: 'home' }
  | { name: 'project'; id: string; desk?: number }
  | { name: 'editor'; id: string }
  | { name: 'tool'; id: string; toolId: string }

export function route(): Route {
  const parts = path().split('/').filter(Boolean).map(decodeURIComponent)
  if (parts[0] !== 'project' || !parts[1]) return { name: 'home' }
  const id = parts[1]
  if (parts[2] === 'editor') return { name: 'editor', id }
  if (parts[2] === 'tool' && parts[3]) return { name: 'tool', id, toolId: parts[3] }
  const n = parts[2] === 'desk' ? parseInt(parts[3], 10) : NaN
  return n > 0 ? { name: 'project', id, desk: n } : { name: 'project', id }
}

/** Path of a desk of a project (desk 1 is the project path). */
export function deskPath(id: string, desk: number) {
  return `/project/${encodeURIComponent(id)}` + (desk > 1 ? `/desk/${desk}` : '')
}
