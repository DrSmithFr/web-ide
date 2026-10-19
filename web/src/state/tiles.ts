// Tiles (docs/design/desks.md): a tool or a console shown alone in a pane of the editor tree, the
// files in groups of tabs. In the Grid mode of a desk they are the whole window, like i3: a new tile opens on the right
// of the focused one or below it, the keyboard moves the focus, swaps and resizes tiles. In
// the Scroll mode, like niri, the columns of the tree go side by side in a strip wider than
// the window: a new tile opens a column (or stacks in the focused one), 1 to 3 are in sight.
import { createEffect, createRoot, createSignal, type JSX } from 'solid-js'
import { createMutable } from 'solid-js/store'
import { activateTab, activeLeaf, addColumn, closeEmptyPane, columnOf, columnsOf, findLeaf, leaves, moveColumn, moveTab, mutate, openTab, project, resizePane, session, setActivePane, setColumnWidth, setTabPlacer, splitEmpty, stackLeft, swapPanes, unstack, type LayoutNode, type TabState } from './project'
import { showTool, zoneOf } from './zones'
import { desk, desks, setDeskMode, type Desk } from './desks'
import { captureKeys } from '../keys/bindings'
import { podPath } from '../app/base'
import { consoles } from '../console/consoles'

export interface TileTool {
  label: string
  icon: string
  component: () => JSX.Element
}

/** The tools that can be tiles (those of the zones, given by the project page). */
export const tileTools: Record<string, TileTool> = createMutable({})
export function registerTileTools(list: Record<string, TileTool>) {
  Object.assign(tileTools, list)
}

const mode = () => desks.desks.find((d) => d.n === desk())?.mode ?? 'full'
/** Whether the desk shown is made of tiles only (Grid or Scroll: no rails nor zones, tools open as tiles). */
export const tileMode = () => mode() !== 'full'
export const scrollMode = () => mode() === 'scroll'

const [viewWidth, setViewWidth] = createSignal(window.innerWidth)
window.addEventListener('resize', () => setViewWidth(window.innerWidth))
/** Columns in sight in the Scroll mode: 1 on a phone, 2 on a tablet, 3 on a desktop. */
export const columnsInSight = () => (viewWidth() <= 720 ? 1 : viewWidth() <= 1280 ? 2 : 3)

/** Widths a column takes in turn (fractions of the view), those at least one column wide. */
const presets = [1 / 3, 1 / 2, 2 / 3, 1]
/** Width of a column: its own, never narrower than one column in sight. */
export const columnWidth = (w: number | undefined) => Math.max(w ?? 0, 1 / columnsInSight())

/** Gives the focused column the next preset width. */
export function cycleWidth() {
  const w = columnWidth(columnsOf()[columnOf(session.activePane)]?.width)
  const next = presets.filter((p) => p >= 1 / columnsInSight() - 1e-6).find((p) => p > w + 1e-6) ?? 1 / columnsInSight()
  setColumnWidth(session.activePane, next)
}

export const moveColumnBy = (d: -1 | 1) => moveColumn(session.activePane, d)
export const stackTile = () => stackLeft(session.activePane)
export const unstackTile = () => unstack(session.activePane)

const paneEl = (id: string) => document.querySelector<HTMLElement>(`.pane[data-pane="${id}"]`)

/** Side where the next tile opens (i3: split horizontal / vertical), saved with the desk. */
export const nextSplit = () => session.split ?? 'row'
export function toggleSplit() {
  mutate((s) => (s.split = nextSplit() === 'row' ? 'col' : 'row'))
}

/** Opens a tab in a new tile beside the focused one, on the side of nextSplit. */
export function openTile(tab: Omit<TabState, 'id'>) {
  const from = activeLeaf()
  // A first tile goes in the empty pane of the desk.
  if (!from.tabs.length) return openTab(tab, from.id)
  return openTab(tab, newPane(from.id))
}

/** A new pane beside another: a new column in the Scroll mode, unless tiles stack (below). */
const newPane = (from: string, side = nextSplit()) => (scrollMode() && side === 'row' ? addColumn(from) : splitEmpty(from, side))

/** Opens a new empty tile (Go to file fills it); it goes away if nothing opens in it. */
export function openEmptyTile(): string {
  const from = activeLeaf()
  if (!from.tabs.length) return from.id
  return newPane(from.id)
}

// ---------- groups of files, tools alone ----------

type Leaf = Extract<LayoutNode, { type: 'leaf' }>

/** Tabs alone in their tile: a tool, a console, and the kanban board in a desk of tiles. */
export const aloneTab = (x: Pick<TabState, 'kind'> | undefined) => !!x && (x.kind === 'tool' || x.kind === 'console' || (x.kind === 'kanban' && tileMode()))
const toolLeaf = (l: Leaf) => l.tabs.some((x) => aloneTab(session.tabs[x]))
const sameTool = (a: Omit<TabState, 'id'> | undefined, b: Omit<TabState, 'id'>) => !!a && a.kind === b.kind && a.tool === b.tool && a.console === b.console

/** The group of files focused last: a file opens there when a tool has the focus. */
let lastFiles = ''
createRoot(() =>
  createEffect(() => {
    const l = findLeaf(session.layout, session.activePane)
    if (l?.tabs.length && !toolLeaf(l)) lastFiles = l.id
  }),
)

setTabPlacer((tab, paneId) => {
  const target = findLeaf(session.layout, paneId) ?? activeLeaf()
  if (aloneTab(tab)) {
    // The Full mode opens no tool as a tab (its zones show them); the kanban board stays a tab there.
    if (!tileMode()) return target.id
    const shown = leaves().find((l) => l.tabs.some((x) => sameTool(session.tabs[x], tab)))
    if (shown) return shown.id
    return target.tabs.length ? newPane(target.id) : target.id
  }
  if (!toolLeaf(target)) return target.id
  const group = findLeaf(session.layout, lastFiles) ?? leaves().find((l) => !toolLeaf(l))
  return group && !toolLeaf(group) ? group.id : newPane(target.id)
})

/** A desk of tiles never mixes: a tool sharing a pane with other tabs goes to a tile of its own, beside. */
function separate() {
  const focus = session.activePane
  for (const l of leaves()) {
    const tools = l.tabs.filter((x) => aloneTab(session.tabs[x]))
    if (l.tabs.length < 2 || !tools.length) continue
    for (const x of tools.length === l.tabs.length ? tools.slice(1) : tools) moveTab(l.id, x, newPane(l.id, 'row'), 0)
  }
  if (findLeaf(session.layout, focus)) setActivePane(focus)
}
createRoot(() =>
  createEffect(() => {
    if (tileMode() && leaves().some((l) => l.tabs.length > 1 && toolLeaf(l))) queueMicrotask(separate)
  }),
)

/** In the Full mode a pane of tools (a tile of a desk of tiles) is hidden: its zones show the tools. */
export function hiddenTools(n: LayoutNode): boolean {
  if (tileMode()) return false
  if (n.type === 'split') return n.children.every(hiddenTools)
  return n.tabs.length > 0 && n.tabs.every((x) => session.tabs[x]?.kind === 'tool' || session.tabs[x]?.kind === 'console')
}

/**
 * Changes the layout mode of a desk. Leaving the tiles for Full shows in their zones the tools
 * that had a tile (the first one of a zone is the one shown) and gives the focus to the files.
 */
export function changeMode(n: number, m: Desk['mode']) {
  const fromTiles = n === desk() && tileMode()
  setDeskMode(n, m)
  if (!fromTiles || m !== 'full') return
  mutate((s) => {
    const zones = new Set<string>()
    for (const l of leaves(s.layout))
      for (const x of l.tabs) {
        const tab = s.tabs[x]
        const id = tab?.kind === 'tool' ? tab.tool : tab?.kind === 'console' ? 'console' : null
        const z = id && zoneOf(s, id)
        if (!id || !z || zones.has(z)) continue
        zones.add(z)
        showTool(s, id)
        if (tab.kind === 'console') s.bottom.active = tab.console!
      }
  })
  // The editor keeps a pane in sight.
  const files = leaves().find((l) => !hiddenTools(l))
  setActivePane(files?.id ?? splitEmpty(session.activePane, 'row'))
}

// ---------- resize ----------

/** Pixels of a resize step (Shift: four steps). */
const step = 12

/** Grows (or shrinks, px < 0) the focused tile: its width (row) or its height (col). */
export function resizeTile(axis: 'row' | 'col', px = step) {
  // A column of the Scroll mode has its own width, a fraction of the view.
  if (scrollMode() && axis === 'row') {
    const strip = document.querySelector('.scroll-strip')?.clientWidth
    const w = columnWidth(columnsOf()[columnOf(session.activePane)]?.width)
    if (strip) setColumnWidth(session.activePane, Math.max(1 / columnsInSight(), Math.min(1, w + px / strip)))
    return
  }
  resizePane(session.activePane, axis, px, (id) => {
    const r = document.querySelector(`.split[data-split="${id}"]`)?.getBoundingClientRect()
    return r ? (axis === 'row' ? r.width : r.height) : 0
  })
}

const [resizing, setResizing] = createSignal(false)
export { resizing }

/**
 * Resize mode, as i3: the arrows grow (right, down) or shrink (left, up) the focused tile,
 * Shift for bigger steps; Escape, Enter or any other key ends it.
 */
export function startResize() {
  setResizing(true)
  captureKeys((e) => {
    if (e.type !== 'keydown' || ['Shift', 'Control', 'Alt', 'Meta'].includes(e.key)) return
    const d = e.shiftKey ? step * 4 : step
    if (e.key === 'ArrowRight') resizeTile('row', d)
    else if (e.key === 'ArrowLeft') resizeTile('row', -d)
    else if (e.key === 'ArrowDown') resizeTile('col', d)
    else if (e.key === 'ArrowUp') resizeTile('col', -d)
    else stopResize()
  })
}

export function stopResize() {
  captureKeys(null)
  setResizing(false)
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
    // In this order: the buttons of the corner come before the content in the page.
    const pane = paneEl(id)
    const sels = ['.md-file-view', '.ed-content', '.xterm-helper-textarea', '.pane-body input, .pane-body textarea, .pane-body [tabindex="0"]', '.pane-body button']
    for (const sel of sels) {
      const el = pane?.querySelector<HTMLElement>(sel)
      if (el) return el.focus()
    }
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

/** Opens a tool or a console tab in a window of its own (as the button of a zone). */
export function detachTab(tab: TabState) {
  if (tab.kind === 'tool' && tab.tool) {
    const id = tab.tool
    window.open(podPath(`/project/${project()!.id}/tool/${id}`), `tool-${id}`, id === 'assistant' ? 'popup,width=1100,height=820' : 'popup,width=420,height=760')
  } else if (tab.kind === 'console' && tab.console) {
    const owner = consoles().find((c) => c.id === tab.console)?.project ?? project()!.id
    window.open(podPath(`/project/${owner}/console/${tab.console}`), `console-${tab.console}`, 'popup,width=900,height=560')
  }
}
