// Tiles (docs/desks.md): a tool or a console shown in a pane of the editor tree, as a tab. In
// the Grid mode of a desk they are the whole window, like i3: a new tile splits the focused
// one along its longest side, the keyboard moves the focus and swaps tiles by direction.
import type { JSX } from 'solid-js'
import { activateTab, activeLeaf, closeEmptyPane, leaves, openTab, session, setActivePane, splitEmpty, swapPanes, type TabState } from './project'
import { desk, desks } from './desks'

export interface TileTool {
  label: string
  icon: string
  component: () => JSX.Element
}

/** The tools that can be tiles (those of the zones, given by the project page). */
export const tileTools: Record<string, TileTool> = {}
export function registerTileTools(list: Record<string, TileTool>) {
  Object.assign(tileTools, list)
}

/** Whether the desk shown is in Grid mode (no rails nor zones: tools open as tiles). */
export const gridMode = () => desks.desks.find((d) => d.n === desk())?.mode === 'grid'

const paneEl = (id: string) => document.querySelector<HTMLElement>(`.pane[data-pane="${id}"]`)

/** Opens a tab in a new tile beside the focused one, split along its longest side. */
export function openTile(tab: Omit<TabState, 'id'>) {
  const from = activeLeaf()
  // A first tile goes in the empty pane of the desk.
  if (!from.tabs.length) return openTab(tab, from.id)
  const r = paneEl(from.id)?.getBoundingClientRect()
  const pane = splitEmpty(from.id, r && r.height > r.width ? 'col' : 'row')
  return openTab(tab, pane)
}

/** Opens a new empty tile (Go to file fills it); it goes away if nothing opens in it. */
export function openEmptyTile(): string {
  const from = activeLeaf()
  if (!from.tabs.length) return from.id
  const r = paneEl(from.id)?.getBoundingClientRect()
  return splitEmpty(from.id, r && r.height > r.width ? 'col' : 'row')
}

export function dropEmptyTile(id: string) {
  if (leaves().length > 1) closeEmptyPane(id)
}

/** Shows a tool as a tile: the tab already showing it, else a new tile. */
export function showToolTile(id: string) {
  for (const l of leaves()) {
    const tab = l.tabs.find((x) => session.tabs[x]?.kind === 'tool' && session.tabs[x]?.tool === id)
    if (tab) {
      activateTab(l.id, tab)
      focusPane(l.id)
      return
    }
  }
  openTile({ kind: 'tool', tool: id })
  focusPane(session.activePane)
}

export type Direction = 'left' | 'right' | 'up' | 'down'

/** The pane next to the active one in a direction: the nearest whose span overlaps it. */
function neighbour(dir: Direction): string | null {
  const from = paneEl(session.activePane)?.getBoundingClientRect()
  if (!from) return null
  let best: { id: string; d: number } | null = null
  for (const l of leaves()) {
    if (l.id === session.activePane) continue
    const r = paneEl(l.id)?.getBoundingClientRect()
    if (!r || !r.width) continue
    const horizontal = dir === 'left' || dir === 'right'
    const overlap = horizontal ? Math.min(r.bottom, from.bottom) - Math.max(r.top, from.top) : Math.min(r.right, from.right) - Math.max(r.left, from.left)
    if (overlap <= 0) continue
    const d = dir === 'left' ? from.left - r.right : dir === 'right' ? r.left - from.right : dir === 'up' ? from.top - r.bottom : r.top - from.bottom
    if (d < -2) continue
    if (!best || d < best.d) best = { id: l.id, d }
  }
  return best?.id ?? null
}

/** Gives the keyboard to the content of a pane: its editor, terminal or first control. */
export function focusPane(id: string) {
  requestAnimationFrame(() => {
    // A popup opened meanwhile (the launcher again) keeps the keyboard.
    if (document.activeElement?.closest('.pick, .modal, .ctx-menu')) return
    const el = paneEl(id)?.querySelector<HTMLElement>('.md-file-view, .ed-content, .xterm-helper-textarea, .pane-body input, .pane-body textarea, .pane-body [tabindex="0"], .pane-body button')
    el?.focus()
  })
}

export function focusDirection(dir: Direction) {
  const id = neighbour(dir)
  if (!id) return false
  setActivePane(id)
  focusPane(id)
}

export function swapDirection(dir: Direction) {
  const id = neighbour(dir)
  if (!id) return false
  swapPanes(session.activePane, id)
  focusPane(id)
}
