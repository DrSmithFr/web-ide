// Minimal router: /, /project/:id, and the detached panels
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
  | { name: 'project'; id: string }
  | { name: 'editor'; id: string }
  | { name: 'tool'; id: string; toolId: string }

export function route(): Route {
  const parts = path().split('/').filter(Boolean).map(decodeURIComponent)
  if (parts[0] !== 'project' || !parts[1]) return { name: 'home' }
  const id = parts[1]
  if (parts[2] === 'editor') return { name: 'editor', id }
  if (parts[2] === 'tool' && parts[3]) return { name: 'tool', id, toolId: parts[3] }
  return { name: 'project', id }
}
