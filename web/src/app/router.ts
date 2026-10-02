// Minimal router: /, /project/:id, and the detached panels
// /project/:id/editor, /project/:id/console/:consoleId, /project/:id/tool/:toolId.
import { createSignal } from 'solid-js'

const [path, setPath] = createSignal(location.pathname)
export { path }

window.addEventListener('popstate', () => setPath(location.pathname))

export function navigate(to: string, replace = false) {
  if (to === location.pathname) return
  if (replace) history.replaceState(null, '', to)
  else history.pushState(null, '', to)
  setPath(to)
}

export type Route =
  | { name: 'home' }
  | { name: 'project'; id: string }
  | { name: 'editor'; id: string }
  | { name: 'console'; id: string; consoleId: string }
  | { name: 'tool'; id: string; toolId: string }

export function route(): Route {
  const parts = path().split('/').filter(Boolean).map(decodeURIComponent)
  if (parts[0] !== 'project' || !parts[1]) return { name: 'home' }
  const id = parts[1]
  if (parts[2] === 'editor') return { name: 'editor', id }
  if (parts[2] === 'console' && parts[3]) return { name: 'console', id, consoleId: parts[3] }
  if (parts[2] === 'tool' && parts[3]) return { name: 'tool', id, toolId: parts[3] }
  return { name: 'project', id }
}
