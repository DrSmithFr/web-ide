// State of the open project: session (tabs, split tree, panels, cursors) shared through the
// pod with every window of the project, the open documents, and the reactions to the
// file events pushed by the pod (remote versions, three-way merge, conflicts).
import { batch, createSignal } from 'solid-js'
import { createStore, produce, reconcile, unwrap } from 'solid-js/store'
import { notify, on, request } from '../pod/rpc'
import { Doc } from '../editor/doc'
import { detectLanguage, lspLanguage, lspLanguageId } from '../editor/languages'
import { merge3 } from '../editor/merge'
import { toast } from '../ui/toast'
import { t, tn } from '../i18n'

export type TabKind = 'file' | 'sql' | 'table' | 'text' | 'diff' | 'kanban' | 'ticket'

export interface TabState {
  id: string
  kind: TabKind
  path?: string
  title?: string
  connId?: string
  db?: string
  table?: string
  text?: string
  lang?: string
  staged?: boolean
  /** Number of the kanban ticket shown (kind ticket). */
  ticket?: number
}

export type LayoutNode =
  | { type: 'leaf'; id: string; tabs: string[]; active: string | null }
  | { type: 'split'; id: string; dir: 'row' | 'col'; children: LayoutNode[]; sizes: number[] }

export interface Cursor {
  anchor: number
  head: number
  scroll: number
}

export interface SessionData {
  layout: LayoutNode
  tabs: Record<string, TabState>
  activePane: string
  cursors: Record<string, Cursor>
  left: { panel: string | null; width: number }
  right: { panel: string | null; width: number }
  /** Strip under the editor: tools of the two bottom zones, split at `split` (fraction). */
  bottom: { left: string | null; right: string | null; height: number; split: number; active: string | null; problemsTab: 'problems' | 'output' }
  sqlText: Record<string, string>
  expanded: string[]
}

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
    bottom: { left: null, right: null, height: 240, split: 0.5, active: null, problemsTab: 'problems' },
    sqlText: {},
    expanded: [],
  }
}

// ---------- reactive state ----------

const [project, setProject] = createSignal<ProjectInfo | null>(null)
const [root, setRoot] = createSignal('')
const [isLocal, setIsLocal] = createSignal(true)
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
  pushTimer = window.setTimeout(() => {
    notify('session.update', { session: unwrap(session) })
  }, 400)
}

export function mutate(fn: (s: SessionData) => void) {
  setSession(produce(fn))
  pushSession()
}

on('session.changed', (data: SessionData) => {
  if (!data || !project()) return
  applyingRemote = true
  setSession(reconcile(normalize(data)))
  applyingRemote = false
  syncDocsWithTabs()
})

function normalize(raw: any): SessionData {
  const d = emptySession()
  if (!raw || typeof raw !== 'object' || !raw.layout) return d
  const s: SessionData = { ...d, ...raw, left: { ...d.left, ...raw.left }, right: { ...d.right, ...raw.right }, bottom: { ...d.bottom, ...raw.bottom } }
  if (s.left.panel && panelAliases[s.left.panel]) {
    s.right.panel = panelAliases[s.left.panel]
    s.left.panel = null
  }
  if (s.right.panel && panelAliases[s.right.panel]) s.right.panel = panelAliases[s.right.panel]
  // Former bottom panel: consoles, problems and output in one strip.
  if (raw.bottom && 'open' in raw.bottom) {
    const { open, order: _order, ...rest } = s.bottom as any
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
  mutate((s) => {
    const l = findLeaf(s.layout, paneId)
    if (l) l.active = tabId
    s.activePane = paneId
  })
}

/** Adds a tab to a pane, or activates the tab already showing the same thing. */
export function openTab(tab: Omit<TabState, 'id'>, paneId = session.activePane): string {
  const leaf = findLeaf(session.layout, paneId) ?? activeLeaf()
  const same = leaf.tabs.find((id) => {
    const t = session.tabs[id]
    if (!t || t.kind !== tab.kind) return false
    if (tab.kind === 'file') return t.path === tab.path
    if (tab.kind === 'table') return t.connId === tab.connId && t.db === tab.db && t.table === tab.table
    if (tab.kind === 'diff') return t.path === tab.path && !!t.staged === !!tab.staged
    if (tab.kind === 'kanban') return true
    if (tab.kind === 'ticket') return t.ticket === tab.ticket
    return false
  })
  if (same) {
    activateTab(leaf.id, same)
    return same
  }
  const id = uid()
  mutate((s) => {
    s.tabs[id] = { ...tab, id }
    const l = findLeaf(s.layout, leaf.id)!
    const at = l.active ? l.tabs.indexOf(l.active) + 1 : l.tabs.length
    l.tabs.splice(at, 0, id)
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
      delete s.sqlText[tabId]
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
      if (src.kind === 'sql') s.sqlText[id] = s.sqlText[src.id] ?? ''
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
      const copy = { ...l, tabs: [...l.tabs] }
      const split: LayoutNode = { type: 'split', id: uid(), dir, children: [copy, fresh], sizes: [0.5, 0.5] }
      if (!parent) s.layout = split
      else parent.children[parent.children.findIndex((c) => c.id === paneId)] = split
    }
    s.activePane = newId
  })
  syncDocsWithTabs()
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

export function relPath(p: string) {
  const r = root()
  if (r && p.startsWith(r + '/')) return p.slice(r.length + 1)
  return p
}

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
      const doc = new Doc(path, f.buffer ?? f.content, { base: f.content, rev: f.rev, readOnly: f.readOnly, lang: detectLanguage(path, f.content) })
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
    const r = await request('fs.write', { path: doc.path, content: text })
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

on('fs.changed', (e: { path: string; content: string; rev: number; saved?: boolean }) => {
  const d = getDoc(e.path)
  if (d) applyRemote(d, e.content, e.rev, e.saved)
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

// ---------- cursors & navigation history ----------

let cursorTimer: number | undefined
const pendingCursors: Record<string, Cursor> = {}
export function saveCursor(path: string, c: Cursor) {
  pendingCursors[path] = c
  clearTimeout(cursorTimer)
  cursorTimer = window.setTimeout(() => {
    const entries = { ...pendingCursors }
    for (const k of Object.keys(pendingCursors)) delete pendingCursors[k]
    mutate((s) => {
      for (const [p, cur] of Object.entries(entries)) s.cursors[p] = cur
      // Keep the cursors of the 300 most recent files.
      const keys = Object.keys(s.cursors)
      if (keys.length > 300) for (const k of keys.slice(0, keys.length - 300)) delete s.cursors[k]
    })
  }, 500)
}

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

const back: NavTarget[] = []
const forward: NavTarget[] = []

export function currentPosition(): NavTarget | null {
  const t = activeTab()
  if (t?.kind !== 'file' || !t.path) return null
  const c = pendingCursors[t.path] ?? session.cursors[t.path]
  return { path: t.path, offset: c?.head ?? 0 }
}

/** Opens a file and moves to a position, recording the jump in the history. */
export async function openFile(target: NavTarget | string, opts: { pane?: string; record?: boolean } = {}) {
  const t: NavTarget = typeof target === 'string' ? { path: target } : target
  if (opts.record !== false) {
    const cur = currentPosition()
    if (cur && (cur.path !== t.path || t.offset !== undefined || t.line !== undefined)) {
      back.push(cur)
      if (back.length > 100) back.shift()
      forward.length = 0
    }
  }
  const title = basename(t.path)
  batch(() => {
    openTab({ kind: 'file', path: t.path, title }, opts.pane)
    if (t.offset !== undefined || t.line !== undefined) setReveal({ path: t.path, target: t, seq: ++revealSeq })
  })
  await loadDoc(t.path)
}

export function navigate(dir: -1 | 1) {
  const from = dir < 0 ? back : forward
  const to = dir < 0 ? forward : back
  const t = from.pop()
  if (!t) return
  const cur = currentPosition()
  if (cur) to.push(cur)
  openFile(t, { record: false })
}

// ---------- project lifecycle ----------

export async function openProject(id: string, creds?: { password?: string; passphrase?: string }) {
  const r = await request('project.open', { id, creds })
  batch(() => {
    setProject({ ...r.project, name: r.project.name })
    setRoot(r.root)
    setIsLocal(r.local)
    applyingRemote = true
    setSession(reconcile(normalize(r.session)))
    applyingRemote = false
  })
  syncDocsWithTabs()
  return r
}

/** After a reconnection: reopen the project and catch up with changes made meanwhile. */
export async function reopenProject() {
  const p = project()
  if (!p) return
  try {
    await request('project.open', { id: p.id })
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
  setProject(null)
  setRoot('')
  setSession(reconcile(emptySession()))
}

/** Virtual (non file) tab: DDL, index definition, LSP hover. */
export function openTextTab(title: string, text: string, lang: string) {
  openTab({ kind: 'text', title, text, lang })
}
