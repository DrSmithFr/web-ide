// Kanban of the project: tickets kept by the pod in SQLite (see docs/kanban.md). The list
// is shared by the board, the side panel and the ticket tabs, and follows the changes
// made by other windows and by the assistant (event kanban.changed).
import { createSignal } from 'solid-js'
import { createStore, reconcile } from 'solid-js/store'
import { on, request } from '../pod/rpc'
import { openTab, project } from '../state/project'
import { t } from '../i18n'

export type Status = 'new' | 'ready' | 'in_progress' | 'review' | 'fix' | 'done' | 'abandoned'
export type TicketType = 'feature' | 'bug' | 'refactor' | 'task'
export type Priority = 'low' | 'normal' | 'high' | 'critical'
export type ChatRole = 'briefing' | 'plan' | 'dev' | 'correction' | 'resolve'

export interface Summary {
  id: number
  title: string
  type: TicketType
  priority: Priority
  status: Status
  branch?: string
  worktree?: string
  goalsDone: number
  goals: number
  chats: number
  created: number
  updated: number
  closed?: number
}

export interface Goal {
  id: number
  text: string
  done: boolean
  source: 'plan' | 'feedback' | 'user'
}

export interface Note {
  id: number
  kind: 'note' | 'feedback' | 'event'
  author: 'user' | 'model'
  text: string
  created: number
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
  base: string
  setup: string
  setupLog?: string
  snapshot?: { base: string; head: string; files: DiffFile[]; patch: string }
  goalList: Goal[]
  notes: Note[]
  files: string[]
  chatList: { chatId: string; role: ChatRole; title: string; created: number }[]
  commits: { hash: string; subject: string }[]
  attachments: { id: number; name: string; mime: string; size: number; created: number }[]
}

export const statusOrder: Status[] = ['new', 'ready', 'in_progress', 'review', 'fix', 'done', 'abandoned']
/** English names (also what the model reads); the *Labels below are translated. */
export const statusNames: Record<Status, string> = {
  new: 'New',
  ready: 'Ready',
  in_progress: 'In progress',
  review: 'To test',
  fix: 'Fix',
  done: 'Done',
  abandoned: 'Abandoned',
}
export const typeNames: Record<TicketType, string> = { feature: 'Feature', bug: 'Bug', refactor: 'Refactor', task: 'Task' }
export const priorityNames: Record<Priority, string> = { low: 'Low', normal: 'Normal', high: 'High', critical: 'Critical' }
export const roleNames: Record<ChatRole, string> = { briefing: 'Briefing', plan: 'Plan', dev: 'Development', correction: 'Correction', resolve: 'Conflicts' }

/** A record whose values are translated when read (reactive in views). */
function translated<K extends string>(names: Record<K, string>): Record<K, string> {
  return new Proxy(names, { get: (o, k) => (typeof k === 'string' && k in o ? t(o[k as K]) : undefined) })
}
export const statusLabels = translated(statusNames)
export const typeLabels = translated(typeNames)
export const priorityLabels = translated(priorityNames)
export const roleLabels = translated(roleNames)

export const [board, setBoard] = createStore<{ project: string; tickets: Summary[]; meta: Record<string, string>; loaded: boolean; error: string }>({
  project: '',
  tickets: [],
  meta: {},
  loaded: false,
  error: '',
})

/** Bumped for a ticket each time it changes: ticket views reload it. */
const [versions, setVersions] = createStore<Record<number, number>>({})
export const ticketVersion = (id: number) => versions[id] ?? 0

let loading: Promise<void> | null = null
export function refreshBoard(): Promise<void> {
  if (loading) return loading
  loading = (async () => {
    try {
      const r = await request<{ project: string; tickets: Summary[]; meta: Record<string, string> }>('kanban.list')
      setBoard('tickets', reconcile(r.tickets, { key: 'id' }))
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
  const pid = project()?.id ?? ''
  if (pid && pid !== loadedFor) {
    loadedFor = pid
    setBoard({ tickets: [], loaded: false })
    refreshBoard()
  }
}

let refreshTimer: number | undefined
on('kanban.changed', (e: { project: string; id: number }) => {
  if (e.id) setVersions(e.id, (v) => (v ?? 0) + 1)
  clearTimeout(refreshTimer)
  refreshTimer = window.setTimeout(() => refreshBoard(), 60)
})
on('pod.reconnected', () => {
  if (loadedFor) refreshBoard()
})

/** A line of the history: the pod stores events as {"key", "params"} (English text and its values). */
export function eventText(text: string): string {
  if (text.startsWith('{')) {
    try {
      const e = JSON.parse(text)
      if (e?.key) {
        const params = { ...e.params }
        for (const k of ['from', 'to'] as const) if (params[k] in statusNames) params[k] = statusLabels[params[k] as Status]
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
  return request<Ticket>('kanban.get', { id })
}

// ---------- changes (by the user; the assistant passes by: 'model') ----------

export type By = 'user' | 'model'

export interface TicketPatch {
  title?: string
  type?: TicketType
  priority?: Priority
  description?: string
  plan?: string
  testSummary?: string
  base?: string
  files?: string[]
  addFiles?: string[]
  removeFiles?: string[]
}

export const createTicket = (p: TicketPatch & { title: string }, by: By = 'user') => request<Ticket>('kanban.create', { ...p, by })
export const updateTicket = (id: number, patch: TicketPatch, by: By = 'user') => request<Ticket>('kanban.update', { id, patch, by })
export const moveTicket = (id: number, status: Status, by: By = 'user', comment = '') => request<Ticket>('kanban.move', { id, status, by, comment })
export const deleteTicket = (id: number) => request('kanban.delete', { id })
export const addNote = (id: number, kind: 'note' | 'feedback', text: string, by: By = 'user') => request<Ticket>('kanban.note', { id, kind, text, by })
export const deleteNote = (id: number, noteId: number) => request<Ticket>('kanban.note.delete', { id, noteId })
export const setPlan = (id: number, plan: string, goals: string[] | null, by: By = 'user') => request<Ticket>('kanban.plan', { id, plan, goals, by })
export const goalOp = (id: number, goal: { op: 'add' | 'check' | 'edit' | 'delete'; id?: number; text?: string; done?: boolean; source?: string }, by: By = 'user') =>
  request<Ticket>('kanban.goal', { id, goal, by })
export const linkChat = (id: number, chatId: string, role: ChatRole, title = '') => request<Ticket>('kanban.chat.link', { id, chatId, role, title })
export const unlinkChat = (id: number, chatId: string) => request<Ticket>('kanban.chat.unlink', { id, chatId })
export const linkCommit = (id: number, hash: string, subject = '', by: By = 'user') => request<Ticket>('kanban.commit.link', { id, hash, subject, by })
export const unlinkCommit = (id: number, hash: string) => request<Ticket>('kanban.commit.unlink', { id, hash })
export const setMeta = (values: Record<string, string>) => request<Record<string, string>>('kanban.meta.set', { values })

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
export const startWork = (id: number, base = '') => request<{ ticket: Ticket; project: string }>('kanban.start', { id, base })
export const worktreeProject = (id: number) => request<{ project: string }>('kanban.open', { id })
export const ticketDiff = (id: number, base = '') => request<Diff | null>('kanban.diff', { id, base })
export const filePatch = (id: number, path: string, from: string, source: string) => request<string>('kanban.diff.file', { id, path, from, source })
export const finishTicket = (id: number, status: 'done' | 'abandoned', comment = '', deleteBranch = false) =>
  request<Ticket>('kanban.finish', { id, status, comment, deleteBranch })

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
}
export const gitInfo = (id: number) => request<GitInfo | null>('kanban.gitstate', { id })
export const mergeTicket = (id: number, squash: boolean) => request<GitInfo>('kanban.merge', { id, squash })
export const rebaseTicket = (id: number) => request<GitInfo>('kanban.rebase', { id })
export const continueGit = (id: number, where: 'worktree' | 'main') => request<GitInfo>('kanban.continue', { id, where })
export const abortGit = (id: number, where: 'worktree' | 'main') => request<GitInfo>('kanban.abort', { id, where })

/** Id of the project opened on the worktree of a ticket (see projects.ChildID). */
export function childProject(ticket: number) {
  const p = project()
  return `${p?.parent || p?.id}-t${ticket}`
}

/**
 * Opens a project (the worktree of a ticket…) in its own window, or brings back the window
 * already open on it (project windows are named after their project).
 */
export function openWorktreeWindow(projectId: string, assistant = false) {
  const url = `/project/${encodeURIComponent(projectId)}${assistant ? '?assistant=1' : ''}`
  const w = window.open('', `project-${projectId}`)
  if (!w) return
  if (assistant || w.location.href === 'about:blank') w.location.href = url
  w.focus()
}

// ---------- tabs ----------

export function openBoard() {
  openTab({ kind: 'kanban', title: 'Kanban' })
}

export function openTicket(id: number) {
  openTab({ kind: 'ticket', ticket: id, title: `#${id}` })
}

export const [newTicketOpen, setNewTicketOpen] = createSignal(false)
