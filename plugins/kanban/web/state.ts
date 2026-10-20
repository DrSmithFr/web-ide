// Kanban of the project: tickets kept by the pod in SQLite (see docs/design/kanban.md). The list
// is shared by the board, the side panel and the ticket tabs, and follows the changes
// made by other windows and by the assistant (event kanban.changed).
import { createSignal } from 'solid-js'
import { createStore, reconcile } from 'solid-js/store'
import { bumpMilestone, type MilestoneSummary } from './milestones'
import { bumpObjective, type ObjectiveSummary } from './objectives'
import { api, on, project, request, t } from './plugin'

export type Status = 'new' | 'todo' | 'in_progress' | 'to_test' | 'done' | 'abandoned'
export type Priority = 'low' | 'normal' | 'high' | 'critical'
/** Estimated effort of a whole ticket, written with its plan. */
export type Size = 's' | 'm' | 'l' | 'xl'
/** How hard a ticket or a feedback is: it picks the model and the effort of its sessions. */
export type Complexity = 'low' | 'medium' | 'high'
export type ChatRole = 'briefing' | 'plan' | 'dev' | 'correction' | 'resolve'
export type FeedbackKind = 'info' | 'bug' | 'feature'

export interface Summary {
  id: number
  title: string
  priority: Priority
  status: Status
  branch?: string
  worktree?: string
  goalsDone: number
  goals: number
  chats: number
  /** Test feedback not handled yet. */
  feedbackOpen: number
  created: number
  updated: number
  closed?: number
  size?: Size
  complexity?: Complexity
  /** Lineage: the parent (developed in its worktree after it), the place among its children. */
  parent?: number
  pos?: number
  /** A parent's own work validated: its first child may start. */
  stepDone?: boolean
  /** Its milestone (a step has the one of its root). */
  milestone?: number
  /** Its objective, apart from its milestone (a step has the one of its root). */
  objective?: number
  /** Tickets of other lineages this one waits for. */
  dependsOn: number[]
  /** What keeps a ticket from starting (New or To do only). */
  blockers?: Blocker[]
  // Board: the goals of the card. Time of the answers of its conversations (sub-agents included).
  goalTitles?: { text: string; done: boolean }[]
  generationMs?: number
}

export type BlockerKind = 'parent' | 'previous' | 'depends' | 'abandoned'
export interface Blocker {
  id: number
  kind: BlockerKind
}
/** English texts of the blockers (also what the model reads). */
export const blockerNames: Record<BlockerKind, string> = {
  parent: 'root not started',
  previous: 'previous step not validated',
  depends: 'dependency not merged',
  abandoned: 'dependency abandoned',
}
export const blockersText = (list: Blocker[] = []) => list.map((b) => `#${b.id} (${blockerNames[b.kind]})`).join(', ')

export interface Goal {
  id: number
  /** Title of the goal. */
  text: string
  description: string
  done: boolean
  source: 'plan' | 'user'
}

/** Who wrote a note or a history line: Claude writes through the MCP endpoint of the pod. */
export type Author = 'user' | 'model' | 'claude'

export interface Note {
  id: number
  kind: 'note' | 'event'
  author: Author
  text: string
  chatId?: string
  created: number
}

export interface Feedback {
  id: number
  kind: FeedbackKind
  text: string
  done: boolean
  author: Author
  chatId?: string
  created: number
  complexity?: Complexity
}

export interface DiffFile {
  path: string
  status: string
  added: number
  removed: number
}

export interface Ticket extends Summary {
  description: string
  plan: string
  testSummary: string
  /** Address of the pull request, once opened. */
  pr?: string
  base: string
  setup: string
  setupLog?: string
  snapshot?: { base: string; head: string; files: DiffFile[]; patch: string }
  goalList: Goal[]
  notes: Note[]
  feedbackList: Feedback[]
  files: string[]
  // Time of the answers: with the sub-agents, of the conversation alone, spent thinking.
  chatList: { chatId: string; role: ChatRole; title: string; created: number; generationMs?: number; ownMs?: number; thinkMs?: number }[]
  attachments: { id: number; name: string; mime: string; size: number; created: number }[]
  /** Children of the lineage, in order. */
  children: Summary[]
  milestoneTitle?: string
  milestoneStories?: { id: number; text: string; done: boolean }[]
  objectiveTitle?: string
}

export const statusOrder: Status[] = ['new', 'todo', 'in_progress', 'to_test', 'done', 'abandoned']
/** English names (also what the model reads); the *Labels below are translated. */
export const statusNames: Record<Status, string> = {
  new: 'New',
  todo: 'To do',
  in_progress: 'In progress',
  to_test: 'To test',
  done: 'Done',
  abandoned: 'Abandoned',
}
/** Statuses of older bases, still named in the history. */
const legacyNames: Record<string, string> = { ready: 'Ready', fix: 'Fix', review: 'To test' }
export const feedbackNames: Record<FeedbackKind, string> = { info: 'Info', bug: 'Bug', feature: 'New feature' }
export const priorityNames: Record<Priority, string> = { low: 'Low', normal: 'Normal', high: 'High', critical: 'Critical' }
export const sizeNames: Record<Size, string> = { s: 'S', m: 'M', l: 'L', xl: 'XL' }
export const complexityNames: Record<Complexity, string> = { low: 'Low complexity', medium: 'Medium complexity', high: 'High complexity' }
export const roleNames: Record<ChatRole, string> = { briefing: 'Briefing', plan: 'Plan', dev: 'Development', correction: 'Correction', resolve: 'Conflicts' }

/** A record whose values are translated when read (reactive in views). */
function translated<K extends string>(names: Record<K, string>): Record<K, string> {
  return new Proxy(names, { get: (o, k) => (typeof k === 'string' && k in o ? t(o[k as K]) : undefined) })
}
export const statusLabels = translated(statusNames)
export const feedbackLabels = translated(feedbackNames)
export const priorityLabels = translated(priorityNames)
export const roleLabels = translated(roleNames)
export const complexityLabels = translated(complexityNames)

export const [board, setBoard] = createStore<{
  project: string
  tickets: Summary[]
  milestones: MilestoneSummary[]
  objectives: ObjectiveSummary[]
  meta: Record<string, string>
  loaded: boolean
  error: string
}>({
  project: '',
  tickets: [],
  milestones: [],
  objectives: [],
  meta: {},
  loaded: false,
  error: '',
})

/** Bumped for a ticket each time it changes: ticket views reload it. */
const [versions, setVersions] = createStore<Record<number, number>>({})
export const ticketVersion = (id: number) => versions[id] ?? 0

let loading: Promise<void> | null = null
/** A pod older than the page still says `review` for To test (update window). */
const fixStatus = <T extends { status: string; children?: { status: string }[] }>(x: T): T => {
  if (x.status === 'review') x.status = 'to_test'
  x.children?.forEach(fixStatus)
  return x
}

export function refreshBoard(): Promise<void> {
  if (loading) return loading
  loading = (async () => {
    try {
      const r = await request<{ project: string; tickets: Summary[]; milestones?: MilestoneSummary[]; objectives?: ObjectiveSummary[]; meta: Record<string, string> }>('kanban.list')
      setBoard('tickets', reconcile(r.tickets.map(fixStatus), { key: 'id' }))
      setBoard('milestones', reconcile(r.milestones ?? [], { key: 'id' }))
      setBoard('objectives', reconcile(r.objectives ?? [], { key: 'id' }))
      setBoard({ project: r.project, meta: r.meta ?? {}, loaded: true, error: '' })
    } catch (e) {
      setBoard({ error: (e as Error).message, loaded: true })
    } finally {
      loading = null
    }
  })()
  return loading
}

/** Loads the board once for the open project (and again when the project changes). */
let loadedFor = ''
export function ensureBoard() {
  // The worktrees of a project share its kanban.
  const pid = project()?.parent || project()?.id || ''
  if (pid && pid !== loadedFor) {
    loadedFor = pid
    setBoard({ tickets: [], milestones: [], objectives: [], loaded: false })
    refreshBoard()
  }
}

/** The board loaded again (the project opened again, a reconnection, the plugin enabled again). */
export function reloadBoard() {
  if (loadedFor) refreshBoard()
}

let refreshTimer: number | undefined
/** Follows the changes of the kanban (once, when the plugin is activated). */
export function watchBoard() {
  on('kanban.changed', (e: { project: string; id: number; milestone?: number; objective?: number }) => {
    if (e.id) setVersions(e.id, (v) => (v ?? 0) + 1)
    if (e.milestone) bumpMilestone(e.milestone)
    if (e.objective) bumpObjective(e.objective)
    clearTimeout(refreshTimer)
    refreshTimer = window.setTimeout(() => refreshBoard(), 60)
  })
}

/** A line of the history: the pod stores events as {"key", "params"} (English text and its values). */
export function eventText(text: string): string {
  if (text.startsWith('{')) {
    try {
      const e = JSON.parse(text)
      if (e?.key) {
        const params = { ...e.params }
        // The pod names the complexity in English (Low, Medium, High).
        const c = String(params.complexity ?? '').toLowerCase()
        if (e.key === 'Complexity: {complexity}' && c in complexityNames) return complexityLabels[c as Complexity]
        for (const k of ['from', 'to'] as const) {
          if (params[k] in statusNames) params[k] = statusLabels[params[k] as Status]
          else if (params[k] in legacyNames) params[k] = t(legacyNames[params[k]])
        }
        return t(e.key, params)
      }
    } catch {
      /* plain text (older bases) */
    }
  }
  return text
}

export function summary(id: number): Summary | undefined {
  return board.tickets.find((t) => t.id === id)
}

export async function getTicket(id: number): Promise<Ticket> {
  return fixStatus(await request<Ticket>('kanban.get', { id }))
}

// ---------- changes (by the user; the assistant passes by: 'model') ----------

export type By = 'user' | 'model'

export interface TicketPatch {
  title?: string
  priority?: Priority
  description?: string
  plan?: string
  testSummary?: string
  base?: string
  files?: string[]
  addFiles?: string[]
  removeFiles?: string[]
  size?: Size | ''
  complexity?: Complexity | ''
  /** 0 takes the ticket out of its lineage. */
  parent?: number
  dependsOn?: number[]
  /** 0 takes the ticket (and its steps) out of its milestone. */
  milestone?: number
  /** 0 takes the ticket (and its steps) out of its objective. */
  objective?: number
}

export const createTicket = (p: TicketPatch & { title: string }, by: By = 'user') => request<Ticket>('kanban.create', { ...p, by })
export const updateTicket = (id: number, patch: TicketPatch, by: By = 'user') => request<Ticket>('kanban.update', { id, patch, by })
export const moveTicket = (id: number, status: Status, by: By = 'user', comment = '', force = false) => request<Ticket>('kanban.move', { id, status, by, comment, force })
export const deleteTicket = (id: number) => request('kanban.delete', { id })
export const addNote = (id: number, text: string, by: By = 'user', chatId = '') => request<Ticket>('kanban.note', { id, text, by, chatId })
export const deleteNote = (id: number, noteId: number) => request<Ticket>('kanban.note.delete', { id, noteId })
export type GoalInput = { title: string; description?: string }
export const setPlan = (id: number, plan: string, goals: GoalInput[] | null, by: By = 'user', size?: Size) => request<Ticket>('kanban.plan', { id, plan, goals, by, size })
export const goalOp = (
  id: number,
  goal: { op: 'add' | 'check' | 'edit' | 'delete'; id?: number; text?: string; description?: string; done?: boolean; source?: string },
  by: By = 'user',
) => request<Ticket>('kanban.goal', { id, goal, by })
export const feedbackOp = (
  id: number,
  feedback: { op: 'add' | 'check' | 'chat' | 'delete' | 'complexity'; id?: number; kind?: FeedbackKind; text?: string; done?: boolean; chatId?: string; complexity?: Complexity | '' },
  by: By = 'user',
) => request<Ticket>('kanban.feedback', { id, feedback, by })

/** Longest texts, in characters (checked by the pod too). */
export const MAX_DESCRIPTION = 1500
export const MAX_NOTE = 1000
export const linkChat = (id: number, chatId: string, role: ChatRole, title = '') => request<Ticket>('kanban.chat.link', { id, chatId, role, title })
export const unlinkChat = (id: number, chatId: string) => request<Ticket>('kanban.chat.unlink', { id, chatId })
export const setMeta = (values: Record<string, string>) =>
  request<Record<string, string>>('kanban.meta.set', { values }).then((meta) => (setBoard('meta', meta ?? {}), meta))

export async function addAttachment(id: number, file: File) {
  const buf = new Uint8Array(await file.arrayBuffer())
  let bin = ''
  for (let i = 0; i < buf.length; i += 0x8000) bin += String.fromCharCode(...buf.subarray(i, i + 0x8000))
  return request<Ticket>('kanban.attachment.add', { id, name: file.name, mime: file.type, data: btoa(bin) })
}
export const deleteAttachment = (id: number, aid: number) => request<Ticket>('kanban.attachment.delete', { id, aid })
export async function attachmentBlob(id: number, aid: number): Promise<{ name: string; blob: Blob }> {
  const r = await request<{ attachment: { name: string; mime: string }; data: string }>('kanban.attachment.get', { id, aid })
  const bin = atob(r.data)
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  return { name: r.attachment.name, blob: new Blob([bytes], { type: r.attachment.mime || 'application/octet-stream' }) }
}

// ---------- git: worktree, changes, end of the ticket ----------

export interface Diff {
  base: string
  from: string
  head: string
  files: DiffFile[]
  source: 'worktree' | 'branch' | 'snapshot'
  ahead: number
  behind: number
  dirty: boolean
}

/** Creates the branch and the worktree of a ticket if needed; returns the project opened on it. */
export const startWork = (id: number, base = '', force = false) => request<{ ticket: Ticket; project: string }>('kanban.start', { id, base, force })
export const worktreeProject = (id: number) => request<{ project: string }>('kanban.open', { id })
export const ticketDiff = (id: number, base = '') => request<Diff | null>('kanban.diff', { id, base })
export const filePatch = (id: number, path: string, from: string, source: string) => request<string>('kanban.diff.file', { id, path, from, source })
export const finishTicket = (id: number, status: 'done' | 'abandoned', comment = '', deleteBranch = false, lineage = false) =>
  request<Ticket>('kanban.finish', { id, status, comment, deleteBranch, lineage })
/** Validates the own work of a parent: its first child may start. */
export const validateStep = (id: number) => request<Ticket>('kanban.step', { id })
export const moveChild = (id: number, delta: number) => request<Ticket>('kanban.child.move', { id, delta })

export interface GitOpState {
  rebase: boolean
  merge: boolean
  squash: boolean
  conflicts: string[]
}
export interface GitInfo {
  worktree?: GitOpState
  main: GitOpState
  into: string
  merged: boolean
  /** A remote origin and the gh command: a pull request can be opened. */
  canPR: boolean
}
export const gitInfo = (id: number) => request<GitInfo | null>('kanban.gitstate', { id })
export const mergeTicket = (id: number, squash: boolean) => request<GitInfo>('kanban.merge', { id, squash })
export const openPR = (id: number) => request<Ticket>('kanban.pr', { id })
export const rebaseTicket = (id: number) => request<GitInfo>('kanban.rebase', { id })
export const continueGit = (id: number, where: 'worktree' | 'main') => request<GitInfo>('kanban.continue', { id, where })
export const abortGit = (id: number, where: 'worktree' | 'main') => request<GitInfo>('kanban.abort', { id, where })

/** The ticket whose worktree a ticket works in: its parent for a step of a lineage. */
export const worktreeOwner = (tk: Summary) => (tk.parent ? tk.parent : tk.id)
/** The window shows the worktree a ticket works in. */
export const inWorktreeOf = (tk: Summary) => !!project()?.ticket && project()?.ticket === worktreeOwner(tk)

/** Id of the project opened on the worktree of a ticket (see projects.ChildID). */
export function childProject(ticket: number) {
  const p = project()
  return `${p?.parent || p?.id}-t${ticket}`
}

// ---------- tabs ----------

export type BoardView = 'board' | 'roadmap' | 'milestones'
const VIEW_KEY = 'webide.kanban.view'
function savedView(): BoardView {
  try {
    const v = localStorage.getItem(VIEW_KEY)
    return v === 'roadmap' || v === 'milestones' ? v : 'board'
  } catch {
    return 'board'
  }
}

/** The view of the board, shared by its tabs and remembered by the browser. */
export const [boardView, setBoardViewSignal] = createSignal<BoardView>(savedView())
export function setBoardView(v: BoardView) {
  setBoardViewSignal(v)
  try {
    localStorage.setItem(VIEW_KEY, v)
  } catch {
    /* private mode: the choice lasts for this window */
  }
}

/** Opens the board, on a view when given. */
export function openBoard(view?: BoardView) {
  if (view) setBoardView(view)
  api.openView('board', { title: 'Kanban' })
}

export function openTicket(id: number) {
  api.openView('ticket', { key: String(id), state: { ticket: id } })
}

export function openMilestone(id: number) {
  api.openView('milestone', { key: String(id), state: { milestone: id } })
}

export function openObjective(id: number) {
  api.openView('objective', { key: String(id), state: { objective: id } })
}

export const [newTicketOpen, setNewTicketOpen] = createSignal(false)

/** A time of work, short: <1 s, 45 s, 12 min, 1 h 05. */
export function workTime(ms: number): string {
  const s = Math.round(ms / 1000)
  if (s < 1) return '<1 s'
  if (s < 60) return `${s} s`
  const m = Math.round(s / 60)
  if (m < 60) return `${m} min`
  return `${Math.floor(m / 60)} h ${String(m % 60).padStart(2, '0')}`
}
