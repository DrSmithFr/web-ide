// Tool zones of the project window. Each icon rail has two groups: the top one opens a side
// panel, the bottom one a tool of the strip under the editor. Each zone shows at most one tool.
import type { SessionData } from './project'

export type Zone = 'left' | 'bottomLeft' | 'bottomRight' | 'right'
export const zones: Zone[] = ['left', 'bottomLeft', 'bottomRight', 'right']

export const defaultPlacement: Record<Zone, string[]> = {
  left: ['explorer', 'search', 'git', 'kanban'],
  bottomLeft: ['console'],
  bottomRight: ['problems'],
  right: ['database', 'assistant', 'structure', 'conflicts', 'info'],
}

export const toolIds = zones.flatMap((z) => defaultPlacement[z])

export function toolsIn(_s: SessionData, zone: Zone): string[] {
  return defaultPlacement[zone]
}

export function zoneOf(s: SessionData, id: string): Zone | undefined {
  return zones.find((z) => toolsIn(s, z).includes(id))
}

/** Tool shown in a zone, null when the zone is closed. */
export function shownIn(s: SessionData, zone: Zone): string | null {
  const id = { left: s.left.panel, right: s.right.panel, bottomLeft: s.bottom.left, bottomRight: s.bottom.right }[zone]
  return id && toolsIn(s, zone).includes(id) ? id : null
}

function setShown(s: SessionData, zone: Zone, id: string | null) {
  if (zone === 'left') s.left.panel = id
  else if (zone === 'right') s.right.panel = id
  else if (zone === 'bottomLeft') s.bottom.left = id
  else s.bottom.right = id
}

/** Shows a tool in its zone (to call inside mutate). */
export function showTool(s: SessionData, id: string) {
  const z = zoneOf(s, id)
  if (z) setShown(s, z, id)
}

/** Shows a tool, or closes its zone when it is already shown. */
export function toggleTool(s: SessionData, id: string) {
  const z = zoneOf(s, id)
  if (z) setShown(s, z, shownIn(s, z) === id ? null : id)
}
