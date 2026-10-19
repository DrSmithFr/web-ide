// State of the open project: session (tabs, split tree, panels, cursors) shared through the
// pod with every window of the project, the open documents, and the reactions to the
// file events pushed by the pod (remote versions, three-way merge, conflicts).
import type { Indent } from '../editor/indent'
import { batch, createEffect, createRoot, createSignal, on as track } from 'solid-js'
import { createStore, produce, reconcile, unwrap } from 'solid-js/store'
import { notify, on, request, setScope } from '../pod/rpc'
import { Doc } from '../editor/doc'
import { detectLanguage, lspLanguage, lspLanguageId } from '../editor/languages'
import { merge3 } from '../editor/merge'
import { toast } from '../ui/toast'
import { t, tn } from '../i18n'
import { normalizePlacement, setPluginZones, type Zone } from './zones'
import { deviceId, layoutFor } from './device'
import { settings } from './settings'
import { closeDesks, desk, loadDesks, showDesk, touchRecent, type DeskList } from './desks'

/** plugin:<plugin>/<view>: a tab view of a plugin (plugins/registry.ts). */
export type TabKind = 'file' | 'text' | 'diff' | 'kanban' | 'ticket' | 'milestone' | 'objective' | 'tool' | 'console' | 'doc' | `plugin:${string}`

export interface TabState {
  id: string
  kind: TabKind
  path?: string
  title?: string
  text?: string
  lang?: string
  /** Diff tabs: index ↔ HEAD (staged), HEAD ↔ working tree (head), or a commit against its first parent (rev); from: former path of a renamed file. */
  staged?: boolean
  head?: boolean
  rev?: string
  from?: string
  /** Number of the kanban ticket shown (kind ticket), of the milestone (kind milestone), or of the objective (kind objective). */
  ticket?: number
  milestone?: number
  objective?: number
  /** A tool shown as a tab or a tile (kind tool: id of the tool) or a console (kind console: its id). */
  tool?: string
  console?: string
  /** Doc tabs: the page of the embedded documentation (path of the repo) and the heading shown. */
  doc?: string
  anchor?: string
  /** Plugin tabs: what tells two tabs of a view apart, and the state the view saves in the desk. */
  key?: string
  state?: unknown
}

/** width: a column of the Scroll mode, its width as a fraction of the view (unset: one column in sight). */
export type LayoutNode =
  | { type: 'leaf'; id: string; tabs: string[]; active: string | null; width?: number }
  | { type: 'split'; id: string; dir: 'row' | 'col'; children: LayoutNode[]; sizes: number[]; width?: number }

export interface Cursor {
  anchor: number
  head: number
  scroll: number
  /** Header lines of the folded ranges. */
  folds?: number[]
  /** Indentation chosen for the file in the status bar. */
  indent?: Indent
}

/**
 * The state of a desk (layout, tabs, consoles). Called session on the wire (session.update,
 * session.changed, the `session` field): a session is now a run of an agent, but the name stays
 * so that a page and a pod of two releases still understand each other.
 */
export interface SessionData {
  layout: LayoutNode
  tabs: Record<string, TabState>
  activePane: string
  cursors: Record<string, Cursor>
  left: { panel: string | null; width: number }
  right: { panel: string | null; width: number }
  /** Tools of each zone of the icon rails (state/zones). */
  placement: Record<Zone, string[]>
  /** Strip under the editor: tools of the two bottom zones, split at `split` (fraction); shown console and order of the console tabs. */
  bottom: { left: string | null; right: string | null; height: number; split: number; active: string | null; order: string[]; problemsTab: 'problems' | 'output' }
  expanded: string[]
  /** Options of the explorer. */
  explorer: { hidden: boolean; excluded: boolean; singleClick: boolean; follow: boolean }
  /** Git tool: shown tab, heights of the commit form and of the commit detail. */
  git: { tab: 'commit' | 'history'; form: number; detail: number }
  /** The state of each plugin in the desk (plugins/api.ts desk()), by plugin id. */
  plugins: Record<string, Record<string, unknown>>
  /** Other worktrees of the repository open in the window, and the one shown (none: the project of the window). */
  attached: string[]
  worktree?: string
  /** Panel sizes per device (state/device.ts): each device keeps its own, the others are not moved. */
  sizes: Record<string, PanelSizes>
  /** Side where a new tile opens: right of the focused one (row) or below it (col). */
  split?: 'row' | 'col'
}

export interface PanelSizes {
  left: number
  right: number
  bottom: number
  split: number
}

const defaultSizes: PanelSizes = { left: 260, right: 300, bottom: 240, split: 0.5 }

export interface ProjectInfo {
  id: string
  name: string
  type: 'local' | 'ssh'
  path: string
  title: string
  description: string
  ssh?: { host: string; port: number; user: string; auth: string; keyPath?: string }
  /** Worktree of a kanban ticket: project it belongs to, and ticket number. */
  parent?: string
  ticket?: number
}

const uid = () => Math.random().toString(36).slice(2, 10)

/** Former panel ids, merged into the Infos tool. */
export const panelAliases: Record<string, string> = { connections: 'info', extensions: 'info', properties: 'info' }

function emptySession(): SessionData {
  const leaf = uid()
  return {
    layout: { type: 'leaf', id: leaf, tabs: [], active: null },
    tabs: {},
    activePane: leaf,
    cursors: {},
    left: { panel: 'explorer', width: 260 },
    right: { panel: null, width: 300 },
    bottom: { left: null, right: null, height: 240, split: 0.5, active: null, order: [], problemsTab: 'problems' },
    placement: normalizePlacement(null),
    expanded: [],
    explorer: { hidden: true, excluded: true, singleClick: false, follow: false },
    git: { tab: 'commit', form: 150, detail: 260 },
    plugins: {},
    attached: [],
    sizes: {},
  }
}

// ---------- reactive state ----------

/**
 * A window opens one project (home: its session) and may show the other worktrees of the
 * same repository without reloading (attached). project, root and isLocal are those of the
 * worktree shown: the explorer, the search, the Git tool and new consoles follow it, while
 * a file or a console stays in the worktree it belongs to.
 */
export interface Attached {
  project: ProjectInfo
  root: string
  local: boolean
}

const [home, setHome] = createSignal<ProjectInfo | null>(null)
const [attached, setAttached] = createStore<Record<string, Attached>>({})
const [project, setProject] = createSignal<ProjectInfo | null>(null)
const [root, setRoot] = createSignal('')
const [isLocal, setIsLocal] = createSignal(true)
export { home, attached }
const [session, setSession] = createStore<SessionData>(emptySession())
const [diagnostics, setDiagnostics] = createStore<Record<string, LspDiagnostic[]>>({})
const [docsVersion, setDocsVersion] = createSignal(0)
export { project, root, isLocal, session, diagnostics, docsVersion }

export interface LspDiagnostic {
  range: { start: { line: number; character: number }; end: { line: number; character: number } }
  severity?: number
  message: string
  source?: string
  code?: string | number
}

// ---------- session sync ----------

let pushTimer: number | undefined
let applyingRemote = false

function pushSession() {
  if (applyingRemote || !project()) return
  clearTimeout(pushTimer)
  pushTimer = window.setTimeout(sendSession, 400)
}

function sendSession() {
  pushTimer = undefined
  notify('session.update', { session: unwrap(session) })
}

export function mutate(fn: (s: SessionData) => void) {
  setSession(produce(fn))
  pushSession()
}

/** The panel sizes in effect for a device: its own entry, else the layout saved for new devices. */
export function sizesFor(device = deviceId): PanelSizes {
  return { ...defaultSizes, ...(session.sizes[device] ?? layoutFor(settings, device).panels ?? {}) }
}

/** Records a panel size for this device: a size moved here does not move the other devices. */
export function setSize(part: keyof PanelSizes, value: number) {
  mutate((s) => {
    const d = s.sizes[deviceId] ?? (s.sizes[deviceId] = { ...defaultSizes })
    d[part] = value
  })
}

on('session.changed', (e: { session: SessionData; desk: number }, from?: string) => {
  if (!e?.session || !project() || (from && from !== home()?.id) || e.desk !== desk()) return
  applyingRemote = true
  setSession(reconcile(normalize(e.session)))
  applyingRemote = false
  syncDocsWithTabs()
})

function normalize(raw: any): SessionData {
  const d = emptySession()
  if (!raw || typeof raw !== 'object' || !raw.layout) return d
  const s: SessionData = { ...d, ...raw, left: { ...d.left, ...raw.left }, right: { ...d.right, ...raw.right }, bottom: { ...d.bottom, ...raw.bottom }, explorer: { ...d.explorer, ...raw.explorer }, git: { ...d.git, ...raw.git }, plugins: { ...raw.plugins }, sizes: { ...raw.sizes } }
  if (s.left.panel && panelAliases[s.left.panel]) {
    s.right.panel = panelAliases[s.left.panel]
    s.left.panel = null
  }
  if (s.right.panel && panelAliases[s.right.panel]) s.right.panel = panelAliases[s.right.panel]
  s.placement = normalizePlacement(raw.placement)
  // The Database explorer became a plugin (#115): its tabs are views of it, a console keeps its text.
  for (const t of Object.values(s.tabs ?? {}) as any[]) {
    if (t.kind === 'sql') {
      Object.assign(t, { kind: 'plugin:database/sql', key: t.id, state: { connId: t.connId, db: t.db ?? '', text: raw.sqlText?.[t.id] ?? '' } })
    } else if (t.kind === 'table') {
      Object.assign(t, { kind: 'plugin:database/table', key: `${t.connId}/${t.db ?? ''}/${t.table}`, state: { connId: t.connId, db: t.db ?? '', table: t.table } })
    } else continue
    delete t.connId
    delete t.db
    delete t.table
  }
  delete (s as any).sqlText
  // The Docker tool became a plugin (#114): its state moved under plugins.
  if (raw.docker && !s.plugins.docker) s.plugins.docker = raw.docker
  delete (s as any).docker
  // The Recent Files moved to the desk list (state/desks.ts), shared by the desks.
  delete (s as any).recent
  if (!Array.isArray(s.attached)) s.attached = []
  // Former bottom panel: consoles, problems and output in one strip.
  if (raw.bottom && 'open' in raw.bottom) {
    const { open, ...rest } = s.bottom as any
    s.bottom = rest
    if (rest.active === 'problems' || rest.active === 'output') {
      s.bottom.problemsTab = rest.active
      s.bottom.active = null
      if (open) s.bottom.right = 'problems'
    } else if (open) s.bottom.left = 'console'
  }
  // Drop the tabs that no pane references and the pane references to missing tabs.
  const used = new Set<string>()
  const walk = (n: LayoutNode) => {
    if (n.type === 'leaf') {
      n.tabs = n.tabs.filter((t) => s.tabs[t])
      n.tabs.forEach((t) => used.add(t))
      if (n.active && !n.tabs.includes(n.active)) n.active = n.tabs[0] ?? null
    } else n.children.forEach(walk)
  }
  walk(s.layout)
  for (const id of Object.keys(s.tabs)) if (!used.has(id)) delete s.tabs[id]
  if (!findLeaf(s.layout, s.activePane)) s.activePane = firstLeaf(s.layout).id
  return s
}

// ---------- layout helpers ----------

export function findLeaf(n: LayoutNode, id: string): Extract<LayoutNode, { type: 'leaf' }> | null {
  if (n.type === 'leaf') return n.id === id ? n : null
  for (const c of n.children) {
    const f = findLeaf(c, id)
    if (f) return f
  }
  return null
}

export function firstLeaf(n: LayoutNode): Extract<LayoutNode, { type: 'leaf' }> {
  return n.type === 'leaf' ? n : firstLeaf(n.children[0])
}

export function leaves(n: LayoutNode = session.layout): Extract<LayoutNode, { type: 'leaf' }>[] {
  return n.type === 'leaf' ? [n] : n.children.flatMap((c) => leaves(c))
}

function parentOf(n: LayoutNode, id: string): Extract<LayoutNode, { type: 'split' }> | null {
  if (n.type === 'leaf') return null
  for (const c of n.children) {
    if (c.id === id) return n
    const p = parentOf(c, id)
    if (p) return p
  }
  return null
}

export function activeLeaf() {
  return findLeaf(session.layout, session.activePane) ?? firstLeaf(session.layout)
}

export function activeTab(): TabState | null {
  const l = activeLeaf()
  return l.active ? session.tabs[l.active] ?? null : null
}

export function setActivePane(id: string) {
  if (session.activePane !== id) mutate((s) => (s.activePane = id))
}

export function activateTab(paneId: string, tabId: string) {
  if (activeTab()?.id !== tabId) record()
  mutate((s) => {
    const l = findLeaf(s.layout, paneId)
    if (l) l.active = tabId
    s.activePane = paneId
  })
}

/** Picks the pane a tab opens in (state/tiles.ts: a document in a group of files, a tool alone). */
let placeTab = (_tab: Omit<TabState, 'id'>, paneId: string) => paneId
export function setTabPlacer(f: typeof placeTab) {
  placeTab = f
}

/** Adds a tab to a pane, or activates the tab already showing the same thing. */
export function openTab(tab: Omit<TabState, 'id'>, paneId = session.activePane): string {
  const leaf = findLeaf(session.layout, placeTab(tab, paneId)) ?? activeLeaf()
  const same = leaf.tabs.find((id) => {
    const t = session.tabs[id]
    if (!t || t.kind !== tab.kind) return false
    if (tab.kind === 'file') return t.path === tab.path
    if (tab.kind === 'diff') return t.path === tab.path && !!t.staged === !!tab.staged && !!t.head === !!tab.head && t.rev === tab.rev
    if (tab.kind === 'kanban') return true
    if (tab.kind === 'ticket') return t.ticket === tab.ticket
    if (tab.kind === 'milestone') return t.milestone === tab.milestone
    if (tab.kind === 'objective') return t.objective === tab.objective
    if (tab.kind === 'tool') return t.tool === tab.tool
    if (tab.kind === 'console') return t.console === tab.console
    if (tab.kind === 'doc') return true
    if (tab.kind.startsWith('plugin:')) return t.key === tab.key
    return false
  })
  if (same) {
    activateTab(leaf.id, same)
    return same
  }
  const id = uid()
  record()
  mutate((s) => {
    s.tabs[id] = { ...tab, id }
    const l = findLeaf(s.layout, leaf.id)!
    // A new tab goes first (a tab already open keeps its place).
    l.tabs.unshift(id)
    l.active = id
    s.activePane = l.id
  })
  syncDocsWithTabs()
  return id
}

export function closeTab(paneId: string, tabId: string, force = false): boolean {
  const tab = session.tabs[tabId]
  if (!tab) return true
  if (!force && tab.kind === 'file' && tab.path) {
    const doc = docs.get(tab.path)?.doc
    const elsewhere = leaves().some((l) => l.tabs.some((t) => t !== tabId && session.tabs[t]?.path === tab.path))
    if (doc?.dirty() && !elsewhere && !confirm(t('{file} has unsaved changes. Close anyway?', { file: basename(tab.path) }))) return false
  }
  mutate((s) => {
    const l = findLeaf(s.layout, paneId)
    if (!l) return
    const i = l.tabs.indexOf(tabId)
    if (i >= 0) l.tabs.splice(i, 1)
    if (l.active === tabId) l.active = l.tabs[Math.min(i, l.tabs.length - 1)] ?? null
    if (!leaves(s.layout).some((x) => x.tabs.includes(tabId))) {
      delete s.tabs[tabId]
    }
    if (l.tabs.length === 0) removeLeaf(s, l.id)
  })
  syncDocsWithTabs()
  return true
}

function removeLeaf(s: SessionData, id: string) {
  const parent = parentOf(s.layout, id)
  if (!parent) return
  const i = parent.children.findIndex((c) => c.id === id)
  parent.children.splice(i, 1)
  parent.sizes.splice(i, 1)
  const total = parent.sizes.reduce((a, b) => a + b, 0) || 1
  parent.sizes = parent.sizes.map((x) => x / total)
  if (parent.children.length === 1) {
    const only = parent.children[0]
    if (parent.width) only.width = parent.width
    const gp = parentOf(s.layout, parent.id)
    if (!gp) s.layout = only
    else gp.children[gp.children.findIndex((c) => c.id === parent.id)] = only
  }
  if (!findLeaf(s.layout, s.activePane)) s.activePane = firstLeaf(s.layout).id
}

/** Splits a pane; the new pane shows the same tab (same buffer, never a copy). */
export function splitPane(paneId: string, dir: 'row' | 'col') {
  const leaf = findLeaf(session.layout, paneId)
  if (!leaf) return
  const newId = uid()
  mutate((s) => {
    const l = findLeaf(s.layout, paneId)!
    let tabs: string[] = []
    let active: string | null = null
    if (l.active) {
      const src = s.tabs[l.active]
      const id = uid()
      s.tabs[id] = { ...structuredClone(unwrap(src)), id }
      tabs = [id]
      active = id
    }
    const fresh: LayoutNode = { type: 'leaf', id: newId, tabs, active }
    const parent = parentOf(s.layout, paneId)
    if (parent && parent.dir === dir) {
      const i = parent.children.findIndex((c) => c.id === paneId)
      const half = parent.sizes[i] / 2
      parent.sizes[i] = half
      parent.children.splice(i + 1, 0, fresh)
      parent.sizes.splice(i + 1, 0, half)
    } else {
      const { width, ...copy } = { ...l, tabs: [...l.tabs] }
      const split: LayoutNode = { type: 'split', id: uid(), dir, children: [copy, fresh], sizes: [0.5, 0.5], width }
      if (!parent) s.layout = split
      else parent.children[parent.children.findIndex((c) => c.id === paneId)] = split
    }
    s.activePane = newId
  })
  syncDocsWithTabs()
}

/** Adds an empty pane beside another one (a new tile), made active; answers its id. */
export function splitEmpty(paneId: string, dir: 'row' | 'col'): string {
  const newId = uid()
  mutate((s) => {
    const l = findLeaf(s.layout, paneId) ?? firstLeaf(s.layout)
    const fresh: LayoutNode = { type: 'leaf', id: newId, tabs: [], active: null }
    const parent = parentOf(s.layout, l.id)
    if (parent && parent.dir === dir) {
      const i = parent.children.findIndex((c) => c.id === l.id)
      const half = parent.sizes[i] / 2
      parent.sizes[i] = half
      parent.children.splice(i + 1, 0, fresh)
      parent.sizes.splice(i + 1, 0, half)
    } else {
      const { width, ...copy } = { ...l, tabs: [...l.tabs] }
      const split: LayoutNode = { type: 'split', id: uid(), dir, children: [copy, fresh], sizes: [0.5, 0.5], width }
      if (!parent) s.layout = split
      else parent.children[parent.children.findIndex((c) => c.id === l.id)] = split
    }
    s.activePane = newId
  })
  return newId
}

/** Removes an empty pane (a tile closed before anything opened in it). */
export function closeEmptyPane(paneId: string) {
  mutate((s) => {
    const l = findLeaf(s.layout, paneId)
    if (l && !l.tabs.length) removeLeaf(s, paneId)
  })
}

/** Exchanges the tabs of two panes (a tile swapped with its neighbour); the focus follows the content. */
export function swapPanes(a: string, b: string) {
  mutate((s) => {
    const x = findLeaf(s.layout, a)
    const y = findLeaf(s.layout, b)
    if (!x || !y) return
    ;[x.tabs, y.tabs] = [y.tabs, x.tabs]
    ;[x.active, y.active] = [y.active, x.active]
    s.activePane = b
  })
}

/**
 * Resizes a pane along an axis (row: its width, col: its height) by px pixels: the nearest
 * split of that direction holding it gives or takes the space of a neighbour; measure gives
 * the size of a split in pixels.
 */
export function resizePane(paneId: string, axis: 'row' | 'col', px: number, measure: (splitId: string) => number) {
  mutate((s) => {
    let child: string = paneId
    let p = parentOf(s.layout, child)
    while (p && p.dir !== axis) {
      child = p.id
      p = parentOf(s.layout, child)
    }
    if (!p) return
    const i = p.children.findIndex((c) => c.id === child)
    const j = i + 1 < p.children.length ? i + 1 : i - 1
    if (j < 0) return
    const total = measure(p.id)
    if (!total) return
    const min = 0.08
    const d = Math.max(min - p.sizes[i], Math.min(px / total, p.sizes[j] - min))
    p.sizes[i] += d
    p.sizes[j] -= d
  })
}

// ---------- columns (Scroll mode) ----------

/** The columns of the Scroll mode: the children of a row split at the root, else the root alone. */
export function columnsOf(n: LayoutNode = session.layout): LayoutNode[] {
  return n.type === 'split' && n.dir === 'row' ? n.children : [n]
}

/** Index of the column holding a pane. */
export function columnOf(paneId: string, n: LayoutNode = session.layout) {
  return Math.max(0, columnsOf(n).findIndex((c) => !!findLeaf(c, paneId)))
}

/** Puts a node as the column i; the root becomes a row split when it is a single column. */
function insertColumn(s: SessionData, node: LayoutNode, i: number) {
  if (s.layout.type !== 'split' || s.layout.dir !== 'row') s.layout = { type: 'split', id: uid(), dir: 'row', children: [unwrap(s.layout)], sizes: [1] }
  const root = s.layout as Extract<LayoutNode, { type: 'split' }>
  root.children.splice(i, 0, node)
  // The Grid mode shares the columns out evenly.
  root.sizes = root.children.map(() => 1 / root.children.length)
}

/** Adds an empty column right of the one holding a pane, made active; answers its pane id. */
export function addColumn(paneId: string): string {
  const id = uid()
  mutate((s) => {
    insertColumn(s, { type: 'leaf', id, tabs: [], active: null }, columnOf(paneId, s.layout) + 1)
    s.activePane = id
  })
  return id
}

/** Moves the column holding a pane one place left (-1) or right (1). */
export function moveColumn(paneId: string, d: -1 | 1) {
  mutate((s) => {
    const cols = columnsOf(s.layout)
    const i = columnOf(paneId, s.layout)
    const j = i + d
    if (cols.length < 2 || j < 0 || j >= cols.length) return
    const [a, b] = [unwrap(cols[i]), unwrap(cols[j])]
    cols[i] = b
    cols[j] = a
  })
}

/** Sets the width of the column holding a pane (a fraction of the view). */
export function setColumnWidth(paneId: string, width: number) {
  mutate((s) => {
    const c = columnsOf(s.layout)[columnOf(paneId, s.layout)]
    if (c) c.width = width
  })
}

/** Stacks a tile at the bottom of the column on its left (its own column goes if empty). */
export function stackLeft(paneId: string) {
  mutate((s) => {
    const i = columnOf(paneId, s.layout)
    if (i === 0) return
    const target = unwrap(columnsOf(s.layout)[i - 1])
    const leaf = findLeaf(s.layout, paneId)
    if (!leaf) return
    const fresh: LayoutNode = { type: 'leaf', id: paneId, tabs: [...leaf.tabs], active: leaf.active }
    removeLeaf(s, paneId)
    const cols = columnsOf(s.layout)
    const k = cols.findIndex((c) => c.id === target.id)
    const col = cols[k]
    if (col.type === 'split' && col.dir === 'col') {
      col.children.push(fresh)
      col.sizes = col.children.map(() => 1 / col.children.length)
    } else {
      const { width, ...inner } = unwrap(col)
      const stack: LayoutNode = { type: 'split', id: uid(), dir: 'col', children: [inner as LayoutNode, fresh], sizes: [0.5, 0.5], width }
      // The last two columns made one: the column is the root.
      if (s.layout.id === col.id) s.layout = stack
      else cols[k] = stack
    }
    s.activePane = paneId
  })
}

/** Takes a tile out of its column into a column of its own, on the right. */
export function unstack(paneId: string) {
  mutate((s) => {
    const i = columnOf(paneId, s.layout)
    const col = columnsOf(s.layout)[i]
    if (!col || col.type === 'leaf') return
    const leaf = findLeaf(s.layout, paneId)
    if (!leaf) return
    const fresh: LayoutNode = { type: 'leaf', id: paneId, tabs: [...leaf.tabs], active: leaf.active }
    removeLeaf(s, paneId)
    insertColumn(s, fresh, i + 1)
    s.activePane = paneId
  })
}

export function moveTab(fromPane: string, tabId: string, toPane: string, index: number) {
  mutate((s) => {
    const a = findLeaf(s.layout, fromPane)
    const b = findLeaf(s.layout, toPane)
    if (!a || !b) return
    const i = a.tabs.indexOf(tabId)
    if (i < 0) return
    a.tabs.splice(i, 1)
    if (a.active === tabId) a.active = a.tabs[Math.min(i, a.tabs.length - 1)] ?? null
    if (fromPane === toPane && i < index) index--
    b.tabs.splice(Math.max(0, Math.min(index, b.tabs.length)), 0, tabId)
    b.active = tabId
    s.activePane = b.id
    if (a.tabs.length === 0 && a.id !== b.id) removeLeaf(s, a.id)
  })
}

export function setSplitSizes(splitId: string, sizes: number[]) {
  const set = (n: LayoutNode): boolean => {
    if (n.type === 'leaf') return false
    if (n.id === splitId) {
      n.sizes = sizes
      return true
    }
    return n.children.some(set)
  }
  mutate((s) => set(s.layout))
}

export function cycleTab(dir: 1 | -1) {
  const l = activeLeaf()
  if (!l.tabs.length) return
  const i = l.active ? l.tabs.indexOf(l.active) : 0
  activateTab(l.id, l.tabs[(i + dir + l.tabs.length) % l.tabs.length])
}

// ---------- documents ----------

interface DocEntry {
  doc: Doc | null
  loading: Promise<Doc | null>
  error?: string
  binary?: boolean
  dispose: () => void
}

const docs = new Map<string, DocEntry>()

export function basename(p: string) {
  return p.slice(p.lastIndexOf('/') + 1)
}

/** Path relative to the worktree holding it. */
export function relPath(p: string) {
  const id = projectOfPath(p)
  const r = id ? attached[id].root : root()
  if (r && p.startsWith(r + '/')) return p.slice(r.length + 1)
  return p
}

// ---------- worktrees ----------

/** The attached worktree holding a path (the deepest root: ticket worktrees live in .ide/worktrees of the main folder). */
export function projectOfPath(path: string): string | undefined {
  let best: Attached | undefined
  for (const a of Object.values(attached)) if ((path === a.root || path.startsWith(a.root + '/')) && (!best || a.root.length > best.root.length)) best = a
  return best?.project.id
}

// Requests on a file run in the worktree of the file; the others in the worktree shown,
// except those of the window itself (its session, its database connections) and of the
// repository (worktrees).
const byPath = /^(fs|git|lsp|buffer|folders)\./
const ownMethods = /^(session|project|worktrees|db|desks?)\./
function pathOf(params: any): string | undefined {
  const p = params?.path ?? params?.params?.textDocument?.uri
  if (typeof p !== 'string') return undefined
  return p.startsWith('file://') ? pathFromUri(p) : p.startsWith('/') ? p : undefined
}
setScope((method, params) => {
  if (ownMethods.test(method)) return undefined
  const p = byPath.test(method) ? pathOf(params) : undefined
  const id = (p && projectOfPath(p)) || project()?.id
  return id && id !== home()?.id ? id : undefined
})

/** Chip of a worktree on the tabs and consoles: its ticket (#12) or its branch; none for the main folder. */
export function worktreeChip(id: string | undefined): { label: string; title: string; hue: number } | null {
  const p = id ? attached[id]?.project : undefined
  if (!p?.parent) return null
  let h = 0
  for (const ch of p.id) h = (h * 31 + ch.charCodeAt(0)) >>> 0
  return { label: p.ticket ? `#${p.ticket}` : p.title || basename(attached[id!].root), title: p.title, hue: h % 360 }
}

/** Opens another worktree of the repository in the window (its files, consoles and git). */
export async function attachWorktree(id: string): Promise<Attached> {
  if (attached[id]) return attached[id]
  const r = await request('project.attach', { id })
  const a: Attached = { project: r.project, root: r.root, local: r.local }
  setAttached(id, a)
  if (id !== home()?.id && !session.attached.includes(id)) mutate((s) => s.attached.push(id))
  return a
}

/** Shows a worktree in the window: the explorer, the search and the Git tool follow it. */
export async function showWorktree(id: string) {
  const a = await attachWorktree(id)
  batch(() => {
    setProject(a.project)
    setRoot(a.root)
    setIsLocal(a.local)
  })
  const w = id === home()?.id ? undefined : id
  if (session.worktree !== w) mutate((s) => (s.worktree = w))
}

/** A worktree closed by the pod (removed, its ticket finished): its tabs close, the window goes back to its project. */
function dropWorktree(id: string) {
  const a = attached[id]
  if (!a) return
  if (project()?.id === id) void showWorktree(home()!.id)
  for (const l of leaves())
    for (const tab of l.tabs) {
      const p = session.tabs[tab]?.path
      if (p && projectOfPath(p) === id) closeTab(l.id, tab)
    }
  setAttached(produce((m) => delete m[id]))
  mutate((s) => {
    s.attached = s.attached.filter((x) => x !== id)
    if (s.worktree === id) s.worktree = undefined
  })
}

on('project.closed', (e: { id: string }) => {
  if (e.id !== home()?.id) dropWorktree(e.id)
})

export function getDoc(path: string): Doc | null {
  return docs.get(path)?.doc ?? null
}

export function docError(path: string) {
  const e = docs.get(path)
  return e?.binary ? 'binary' : e?.error
}

export function openDocs(): Doc[] {
  docsVersion()
  return [...docs.values()].map((e) => e.doc).filter((d): d is Doc => !!d)
}

const uri = (p: string) => 'file://' + p.split('/').map(encodeURIComponent).join('/')
export const pathFromUri = (u: string) => decodeURIComponent(u.replace(/^file:\/\//, ''))
export { uri as fileUri }

/** Loads a document (once per path, shared by every tab showing it). */
export function loadDoc(path: string): Promise<Doc | null> {
  const known = docs.get(path)
  if (known) return known.loading
  const entry: DocEntry = { doc: null, loading: null as any, dispose: () => {} }
  entry.loading = (async () => {
    try {
      const f = await request('fs.read', { path })
      if (f.binary) {
        entry.binary = true
        return null
      }
      const doc = new Doc(path, f.buffer ?? f.content, {
        base: f.content,
        rev: f.rev,
        readOnly: f.readOnly,
        lang: detectLanguage(path, f.content),
        format: { encoding: f.encoding ?? 'utf-8', eol: f.eol ?? 'lf' },
      })
      entry.doc = doc
      entry.dispose = attachDoc(doc)
      setDocsVersion((v) => v + 1)
      return doc
    } catch (e) {
      entry.error = (e as Error).message
      setDocsVersion((v) => v + 1)
      return null
    }
  })()
  docs.set(path, entry)
  return entry.loading
}

/** Wires a document to the pod: shared buffer between windows, language server. */
const lspFlush = new Map<string, () => void>()

/** Sends the pending document change to the language server now (before a request). */
export function flushLsp(path: string) {
  lspFlush.get(path)?.()
}

function attachDoc(doc: Doc): () => void {
  const lang = lspLanguage(doc.path)
  let syncTimer: number | undefined
  let lspTimer: number | undefined
  let lspPending = false
  const sendChange = () => {
    clearTimeout(lspTimer)
    if (!lspPending) return
    lspPending = false
    notify('lsp.notify', { lang, method: 'textDocument/didChange', params: { textDocument: { uri: uri(doc.path), version: 0 }, contentChanges: [{ text: doc.text }] } })
  }
  lspFlush.set(doc.path, sendChange)
  if (lang && !doc.readOnly) {
    notify('lsp.notify', { lang, method: 'textDocument/didOpen', params: { textDocument: { uri: uri(doc.path), languageId: lspLanguageId(doc.path), version: 1, text: doc.text } } })
  }
  const off = doc.onChange((c) => {
    if (lang && !doc.readOnly) {
      lspPending = true
      clearTimeout(lspTimer)
      lspTimer = window.setTimeout(sendChange, 250)
    }
    if (c.origin === 'remote' || c.origin === 'sync') return
    clearTimeout(syncTimer)
    syncTimer = window.setTimeout(() => {
      notify('buffer.sync', { path: doc.path, content: doc.dirty() ? doc.text : null })
    }, 300)
  })
  return () => {
    off()
    lspFlush.delete(doc.path)
    clearTimeout(syncTimer)
    clearTimeout(lspTimer)
    if (lang && !doc.readOnly) notify('lsp.notify', { lang, method: 'textDocument/didClose', params: { textDocument: { uri: uri(doc.path) } } })
  }
}

/** Loads the documents of the open tabs and forgets the others. */
export function syncDocsWithTabs() {
  const wanted = new Set<string>()
  for (const t of Object.values(session.tabs)) if (t.kind === 'file' && t.path) wanted.add(t.path)
  for (const p of wanted) if (!docs.has(p)) loadDoc(p)
  for (const [p, e] of docs) {
    if (!wanted.has(p) && !e.doc?.dirty()) {
      e.dispose()
      docs.delete(p)
    }
  }
  setDocsVersion((v) => v + 1)
}

export async function saveDoc(doc: Doc): Promise<boolean> {
  if (doc.readOnly) {
    toast(t('Read-only file (outside the project)'), 'warn')
    return false
  }
  if (doc.conflict()) {
    toast(t('{file} is in conflict: resolve it before saving (Ctrl+Alt+R)', { file: basename(doc.path) }), 'warn')
    return false
  }
  const text = doc.text
  try {
    const r = await request('fs.write', { path: doc.path, content: text, ...doc.format() })
    doc.setBase(text, r.rev)
    doc.setDeleted(false)
    const lang = lspLanguage(doc.path)
    if (lang) notify('lsp.notify', { lang, method: 'textDocument/didSave', params: { textDocument: { uri: uri(doc.path) } } })
    return true
  } catch (e) {
    toast(t('Cannot save {file}: {message}', { file: basename(doc.path), message: (e as Error).message }), 'error')
    return false
  }
}

export async function saveAll() {
  let n = 0
  for (const d of openDocs()) if (d.dirty() && (await saveDoc(d))) n++
  if (n) toast(tn(n, '{n} file saved', '{n} files saved'), 'ok')
}

/**
 * A new version of the file arrived from the pod (AI edit, other tool, other window).
 * Clean files take it silently; modified ones go through a three-way merge; when the
 * merge has conflicts the local buffer is kept until the user decides.
 */
export function applyRemote(doc: Doc, content: string, rev: number, saved = false) {
  doc.setDeleted(false)
  if (content === doc.base) {
    doc.baseRev = rev
    if (doc.conflict()) doc.setConflict(null)
    return
  }
  const local = doc.text
  if (local === doc.base || local === content) {
    doc.setText(content, 'remote')
    doc.setBase(content, rev)
    doc.setConflict(null)
    if (!saved) toast(t('{file} updated', { file: basename(doc.path) }), 'info')
    return
  }
  const m = merge3(local, doc.base, content)
  if (m.clean) {
    doc.setText(m.text, 'remote')
    doc.setBase(content, rev)
    doc.setConflict(null)
    toast(t('New version of {file} merged with your changes', { file: basename(doc.path) }), 'ok')
  } else {
    doc.setConflict({ remote: content, rev })
    toast(t('Conflict on {file}: your changes are kept', { file: basename(doc.path) }), 'warn', {
      label: t('Resolve'),
      run: () => openConflictHook?.(doc),
    })
  }
}

/** Result of the conflict dialog: the merged text becomes the buffer, remote the new base. */
export function resolveConflict(doc: Doc, text: string) {
  const c = doc.conflict()
  if (!c) return
  doc.setText(text, doc)
  doc.setBase(c.remote, c.rev)
  doc.setConflict(null)
}

let openConflictHook: ((doc: Doc) => void) | null = null
export function setConflictOpener(f: (doc: Doc) => void) {
  openConflictHook = f
}

export function conflictedDocs(): Doc[] {
  return openDocs().filter((d) => d.conflict())
}

on('fs.changed', (e: { path: string; content: string; rev: number; saved?: boolean; encoding?: string; eol?: 'lf' | 'crlf' }) => {
  const d = getDoc(e.path)
  if (!d) return
  if (e.encoding && e.eol) d.setFormat({ encoding: e.encoding, eol: e.eol })
  applyRemote(d, e.content, e.rev, e.saved)
})
// A file link of Claude Code (/open on the pod): line counts from 1.
on('ide.open', (e: { path: string; line: number }) => {
  void openFile(e.line > 0 ? { path: e.path, line: e.line - 1 } : e.path)
  window.focus()
})

on('fs.deleted', (e: { path: string }) => {
  const d = getDoc(e.path)
  if (d) {
    d.setDeleted(true)
    toast(t('{file} was deleted from the disk', { file: basename(e.path) }), 'warn')
  }
})
on('buffer.synced', (e: { path: string; content: string | null }) => {
  const d = getDoc(e.path)
  if (d) d.setText(e.content ?? d.base, 'sync')
})
on('lsp.diagnostics', (e: { params: { uri: string; diagnostics: LspDiagnostic[] } }) => {
  const p = pathFromUri(e.params.uri)
  setDiagnostics(p, e.params.diagnostics ?? [])
})

// ---------- recent files ----------

createRoot(() =>
  createEffect(
    track(
      () => (project() && activeTab()?.kind === 'file' ? activeTab()!.path! : null),
      (path) => path && touchRecent(path),
    ),
  ),
)

// ---------- cursors & navigation history ----------

let cursorTimer: number | undefined
const pendingCursors: Record<string, Cursor> = {}
export function saveCursor(path: string, c: Cursor) {
  pendingCursors[path] = c
  clearTimeout(cursorTimer)
  cursorTimer = window.setTimeout(storeCursors, 500)
}

function storeCursors() {
  cursorTimer = undefined
  const entries = { ...pendingCursors }
  for (const k of Object.keys(pendingCursors)) delete pendingCursors[k]
  mutate((s) => {
    for (const [p, cur] of Object.entries(entries)) s.cursors[p] = cur
    // Keep the cursors of the 300 most recent files.
    const keys = Object.keys(s.cursors)
    if (keys.length > 300) for (const k of keys.slice(0, keys.length - 300)) delete s.cursors[k]
  })
}

// A reload or a closed window sends the changes still waiting, the cursors included.
window.addEventListener('pagehide', () => {
  if (cursorTimer !== undefined) {
    clearTimeout(cursorTimer)
    storeCursors()
  }
  if (pushTimer !== undefined) {
    clearTimeout(pushTimer)
    sendSession()
  }
})

export interface NavTarget {
  path: string
  offset?: number
  line?: number
  col?: number
  end?: number
}

/** Requested positions, consumed by the editor pane once the document is shown. */
const [reveal, setReveal] = createSignal<{ path: string; target: NavTarget; seq: number } | null>(null)
export { reveal }
let revealSeq = 0

/** A place of the history: a position in a file, or another tab (board, table, diff…). */
type Place = NavTarget | { tab: string }

const back: Place[] = []
const forward: Place[] = []
let replaying = false // navigate moves without recording

export function currentPosition(): NavTarget | null {
  const t = activeTab()
  if (t?.kind !== 'file' || !t.path) return null
  const c = pendingCursors[t.path] ?? session.cursors[t.path]
  return { path: t.path, offset: c?.head ?? 0 }
}

function currentPlace(): Place | null {
  const t = activeTab()
  return t ? (currentPosition() ?? { tab: t.id }) : null
}

const samePlace = (a: Place, b: Place) => ('tab' in a ? 'tab' in b && a.tab === b.tab : !('tab' in b) && a.path === b.path && a.offset === b.offset)

/** Records the place shown before a jump or a change of tab. */
function record() {
  const cur = currentPlace()
  if (replaying || !cur) return
  const last = back[back.length - 1]
  if (!last || !samePlace(last, cur)) back.push(cur)
  if (back.length > 100) back.shift()
  forward.length = 0
}

/** Opens a file and moves to a position, recording the jump in the history. */
export async function openFile(target: NavTarget | string, opts: { pane?: string; record?: boolean } = {}) {
  const t: NavTarget = typeof target === 'string' ? { path: target } : target
  if (opts.record !== false) {
    const cur = currentPosition()
    if (!cur || cur.path !== t.path || t.offset !== undefined || t.line !== undefined) record()
  }
  const title = basename(t.path)
  batch(() => {
    openTab({ kind: 'file', path: t.path, title }, opts.pane)
    if (t.offset !== undefined || t.line !== undefined) setReveal({ path: t.path, target: t, seq: ++revealSeq })
  })
  await loadDoc(t.path)
}

/** Moves back (-1) or forward (1) in the history of the editor: positions and tabs. */
export function navigate(dir: -1 | 1) {
  const from = dir < 0 ? back : forward
  const to = dir < 0 ? forward : back
  let t = from.pop()
  while (t && 'tab' in t && !session.tabs[t.tab]) t = from.pop() // a tab closed since
  if (!t) return
  const cur = currentPlace()
  if (cur) to.push(cur)
  replaying = true
  try {
    if (!('tab' in t)) void openFile(t, { record: false })
    else {
      const id = t.tab
      const l = leaves().find((x) => x.tabs.includes(id))
      if (l) activateTab(l.id, id)
    }
  } finally {
    replaying = false
  }
}

// ---------- project lifecycle ----------

export async function openProject(id: string, creds?: { password?: string; passphrase?: string }, desk = 1) {
  const r = await request('project.open', { id, creds, desk })
  loadDesks(id, r.desks as DeskList, r.desk ?? 1)
  setPluginZones(r.plugins)
  batch(() => {
    setHome(r.project)
    setAttached(reconcile({ [id]: { project: r.project, root: r.root, local: r.local } }))
    setProject(r.project)
    setRoot(r.root)
    setIsLocal(r.local)
    applyingRemote = true
    setSession(reconcile(normalize(r.session)))
    applyingRemote = false
  })
  await reattach()
  syncDocsWithTabs()
  return r
}

/** Attaches again the worktrees the window had (those gone are forgotten) and shows the one it showed. */
async function reattach() {
  const gone: string[] = []
  for (const w of [...session.attached]) await attachWorktree(w).catch(() => gone.push(w))
  if (gone.length) mutate((s) => (s.attached = s.attached.filter((x) => !gone.includes(x))))
  const w = session.worktree
  if (w && attached[w]) await showWorktree(w)
  else {
    if (w) mutate((s) => (s.worktree = undefined))
    if (project()?.id !== home()?.id) await showWorktree(home()!.id)
  }
}

/** Sends the session now if a change waits (before the window leaves its desk). */
async function flushSession() {
  if (pushTimer === undefined) return
  clearTimeout(pushTimer)
  pushTimer = undefined
  await request('session.update', { session: unwrap(session) })
}

/** Shows another desk of the project in the window: its session replaces this one, the open documents stay. */
export async function switchDesk(n: number) {
  if (!home() || n === desk()) return
  await flushSession()
  const r = await request('desk.switch', { desk: n })
  batch(() => {
    showDesk(r.desk)
    applyingRemote = true
    setSession(reconcile(normalize(r.session)))
    applyingRemote = false
  })
  await reattach()
  syncDocsWithTabs()
}

/** Sends a tab to another desk: it opens there in the active pane and closes here. */
export async function moveTabToDesk(paneId: string, tabId: string, n: number) {
  const tab = session.tabs[tabId]
  if (!tab || n === desk()) return
  const target = normalize(await request('session.get', { desk: n }))
  const pane = findLeaf(target.layout, target.activePane) ?? firstLeaf(target.layout)
  const same = Object.values(target.tabs).find((x) => x.kind === tab.kind && x.path && x.path === tab.path && !x.staged && !x.head && !x.rev && !tab.staged && !tab.head && !tab.rev)
  if (same) {
    const l = leaves(target.layout).find((x) => x.tabs.includes(same.id))!
    l.active = same.id
    target.activePane = l.id
  } else {
    target.tabs[tabId] = structuredClone(unwrap(tab))
    if (tab.path && session.cursors[tab.path]) target.cursors[tab.path] = structuredClone(unwrap(session.cursors[tab.path]))
    pane.tabs.push(tabId)
    pane.active = tabId
  }
  await request('session.put', { desk: n, session: target })
  closeTab(paneId, tabId, true)
}

/** After a reconnection: reopen the project and catch up with changes made meanwhile. */
export async function reopenProject() {
  const p = home()
  if (!p) return
  try {
    await request('project.open', { id: p.id, desk: desk() })
    for (const w of Object.keys(attached)) if (w !== p.id) await request('project.attach', { id: w }).catch(() => dropWorktree(w))
  } catch {
    return
  }
  for (const [path, e] of docs) {
    if (!e.doc) continue
    try {
      const f = await request('fs.read', { path })
      if (!f.binary && f.content !== e.doc.base) applyRemote(e.doc, f.content, f.rev)
    } catch {
      e.doc.setDeleted(true)
    }
  }
}

export function closeProject() {
  for (const e of docs.values()) e.dispose()
  docs.clear()
  setHome(null)
  setAttached(reconcile({}))
  setProject(null)
  setRoot('')
  setSession(reconcile(emptySession()))
  closeDesks()
}

/** Virtual (non file) tab: DDL, index definition, LSP hover. */
export function openTextTab(title: string, text: string, lang: string) {
  openTab({ kind: 'text', title, text, lang })
}
