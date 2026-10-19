// Tool zones of the project window. Each icon rail has two groups: the top one opens a side
// panel, the bottom one a tool of the strip under the editor. Each zone shows at most one tool.
// Icons are dragged between groups; the placement is saved in the session. The tools of the
// plugins come after those of the core in their default zone; a disabled plugin keeps its place.
import type { SessionData } from './project'

export type Zone = 'left' | 'bottomLeft' | 'bottomRight' | 'right'
export const zones: Zone[] = ['left', 'bottomLeft', 'bottomRight', 'right']

const corePlacement: Record<Zone, string[]> = {
  left: [],
  bottomLeft: [],
  bottomRight: [],
  right: ['assistant', 'agents', 'questions', 'conflicts', 'info'],
}

// The tools of every plugin the pod knows, enabled or not (project.open).
let pluginZones: { id: string; zone: Zone; before?: string }[] = []
export function setPluginZones(plugins: { manifest: { contributes: { tools?: { id: string; zone: Zone; before?: string }[] } } }[] | undefined) {
  pluginZones = (plugins ?? []).flatMap((p) => p.manifest.contributes.tools ?? []).filter((t) => zones.includes(t.zone))
}

export function defaultPlacement(): Record<Zone, string[]> {
  const out = {} as Record<Zone, string[]>
  for (const z of zones) {
    const mine = pluginZones.filter((x) => x.zone === z)
    const list = [...corePlacement[z], ...mine.map((t) => t.id)]
    // Each tool goes before the one it names, which may be a plugin's too: moved until stable.
    for (let pass = 0, moved = true; moved && pass <= mine.length; pass++) {
      moved = false
      for (const t of mine) {
        const i = list.indexOf(t.id)
        const j = t.before ? list.indexOf(t.before) : -1
        if (j >= 0 && i > j) {
          list.splice(i, 1)
          list.splice(j, 0, t.id)
          moved = true
        }
      }
    }
    out[z] = list
  }
  return out
}

export const toolIds = () => zones.flatMap((z) => defaultPlacement()[z])

export function toolsIn(s: SessionData, zone: Zone): string[] {
  return s.placement[zone]
}

/** Saved placement checked: unknown ids dropped, missing tools back in their default zone. */
export function normalizePlacement(raw: any): Record<Zone, string[]> {
  const seen = new Set<string>()
  const out = {} as Record<Zone, string[]>
  const defaults = defaultPlacement()
  const known = zones.flatMap((z) => defaults[z])
  for (const z of zones) {
    const list: unknown[] = Array.isArray(raw?.[z]) ? raw[z] : defaults[z]
    out[z] = list.filter((id): id is string => typeof id === 'string' && known.includes(id) && !seen.has(id) && !!seen.add(id))
  }
  for (const z of zones) for (const id of defaults[z]) if (!seen.has(id)) out[z].push(id)
  return out
}

/** Moves a tool to a zone, before the tool at `index`; a shown tool stays shown. */
export function moveTool(s: SessionData, id: string, zone: Zone, index: number) {
  const from = zoneOf(s, id)
  if (!from) return
  const shown = shownIn(s, from) === id
  s.placement[from] = s.placement[from].filter((x) => x !== id)
  const list = s.placement[zone]
  s.placement[zone] = [...list.slice(0, index), id, ...list.slice(index)]
  if (shown && from !== zone) {
    setShown(s, from, null)
    setShown(s, zone, id)
  }
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
