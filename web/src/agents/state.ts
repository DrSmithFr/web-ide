// State of the Agents tool: the ACP sessions of the worktrees of the window (pod: internal/acp),
// their threads loaded when a tab is shown and kept up to date by the events of the pod.
import { createEffect, createRoot, createSignal, on } from 'solid-js'
import { createStore, produce, reconcile } from 'solid-js/store'
import { on as onPod, requestIn } from '../pod/rpc'
import { attached, home } from '../state/project'
import type { DiffLine } from '../llm/state'
import { lineHunks } from '../editor/linediff'
import type { Totals } from './stats'

export interface ConfigOption {
  id: string
  name: string
  category?: string
  type: string
  currentValue: string
  options?: { value: string; name: string; description?: string }[] | { group: string; name: string; options: { value: string; name: string }[] }[]
}

export interface AgentInfo {
  id: string
  agent: string
  name: string
  title: string
  cwd: string
  state: 'starting' | 'ready' | 'busy' | 'asking' | 'error'
  error?: string
  crashed?: boolean
  config?: ConfigOption[]
  usage?: { used: number; size: number }
  /** The prompts waiting for the end of the turn, and what a prompt may hold. */
  queue?: { text: string; attachments?: number }[]
  caps?: { image: boolean; embeddedContext: boolean }
  /** The slash commands the agent announces. */
  commands?: { name: string; description: string; input?: { hint?: string } }[]
  /** Work time and cost of its turns (agents/stats.tsx). */
  stats?: Totals
  started: number
  /** Ticket the session develops (kanban), 0 or absent for none. */
  ticket?: number
  seq: number
  /** Worktree the session runs in (set by the page). */
  project?: string
}

export interface ToolContent {
  type: 'content' | 'diff' | 'terminal'
  text?: string
  path?: string
  oldText?: string | null
  newText?: string
}

export interface Entry {
  kind: 'user' | 'agent' | 'thought' | 'tool' | 'plan' | 'permission' | 'notice' | 'error'
  text?: string
  /** Images (a file of the session) and files of a user prompt. */
  attachments?: { name: string; kind: 'image' | 'text'; file?: string; mime?: string }[]
  time: number
  toolId?: string
  title?: string
  toolKind?: string
  status?: string
  content?: ToolContent[]
  locations?: string[]
  plan?: { content: string; priority?: string; status?: string }[]
  options?: { optionId: string; name: string; kind: string }[]
  outcome?: string
}

export interface CatalogAgent {
  id: string
  name: string
  package?: string
  hint?: string
  installed: boolean
  missing?: string
}

const [sessions, setSessions] = createSignal<AgentInfo[]>([])
export { sessions }
const [active, setActive] = createSignal<string | null>(null)
export { active, setActive }

/** Threads of the sessions loaded, by id. */
const [threads, setThreads] = createStore<Record<string, { seq: number; entries: Entry[] }>>({})
export { threads }

export const projectOf = (id: string) => sessions().find((s) => s.id === id)?.project

function upsert(info: AgentInfo, project?: string) {
  setSessions((l) => {
    const i = l.findIndex((s) => s.id === info.id)
    if (i < 0) return [...l, { ...info, project }]
    if (info.seq < l[i].seq) return l
    const next = [...l]
    next[i] = { ...info, project: l[i].project }
    return next
  })
}

/** Sessions of a worktree of the window (its project by default). */
function setList(list: AgentInfo[], pid = home()?.id) {
  setSessions((l) => [...l.filter((s) => s.project !== pid), ...list.map((s) => ({ ...s, project: pid }))])
}

export async function refreshAgents() {
  for (const pid of Object.keys(attached)) await requestIn<AgentInfo[]>(pid, 'agents.list').then((l) => setList(l, pid), () => {})
  for (const id of Object.keys(threads)) void loadThread(id)
}

createRoot(() =>
  createEffect(
    on(
      () => Object.keys(attached),
      (ids, prev) => {
        for (const pid of ids) if (!prev?.includes(pid)) requestIn<AgentInfo[]>(pid, 'agents.list').then((l) => setList(l, pid), () => {})
        for (const pid of prev ?? []) if (!ids.includes(pid)) setList([], pid)
      },
    ),
  ),
)

export async function loadThread(id: string) {
  const r = await requestIn<{ info: AgentInfo; entries: Entry[] }>(projectOf(id), 'agents.get', { id })
  upsert(r.info)
  setThreads(id, reconcile({ seq: r.info.seq, entries: r.entries ?? [] }))
}

onPod('agents.created', (info: AgentInfo, from) => upsert(info, from ?? home()?.id))
onPod('agents.changed', (info: AgentInfo, from) => upsert(info, from ?? home()?.id))
onPod('agents.closed', (e: { id: string }) => {
  setSessions((l) => l.filter((s) => s.id !== e.id))
  setThreads(produce((t) => delete t[e.id]))
  if (active() === e.id) setActive(null)
})
// An event older than the thread loaded is already in it.
onPod('agents.entry', (e: { id: string; seq: number; index: number; entry: Entry }) => {
  const th = threads[e.id]
  if (!th || e.seq <= th.seq) return
  setThreads(e.id, produce((t) => {
    t.seq = e.seq
    t.entries[e.index] = e.entry
  }))
})
onPod('agents.append', (e: { id: string; seq: number; index: number; text: string }) => {
  const th = threads[e.id]
  if (!th || e.seq <= th.seq || !th.entries[e.index]) return
  setThreads(e.id, produce((t) => {
    t.seq = e.seq
    t.entries[e.index].text = (t.entries[e.index].text ?? '') + e.text
  }))
})

// ---------- actions ----------

const call = <T = unknown>(id: string, method: string, params: Record<string, unknown> = {}) => requestIn<T>(projectOf(id), method, { id, ...params })

/** A piece of a prompt besides its text: an image (base64) or the text of a file. */
export interface AgentPart {
  kind: 'image' | 'text'
  name: string
  mime?: string
  data?: string
  text?: string
}

export const promptAgent = (id: string, text: string, parts: AgentPart[] = []) => call(id, 'agents.prompt', { text, parts })
export const unqueueAgent = (id: string, index: number) => call<{ text: string }>(id, 'agents.unqueue', { index })
export const agentImage = (id: string, file: string) => call<string>(id, 'agents.image', { file })
export const cancelAgent = (id: string) => call(id, 'agents.cancel')
export const answerAgent = (id: string, index: number, option: string) => call(id, 'agents.answer', { index, option })
export const configAgent = (id: string, config: string, value: string) => call(id, 'agents.config', { config, value })
export const restartAgent = (id: string) => call(id, 'agents.restart')
export const closeAgent = (id: string) => call(id, 'agents.close')

export async function createAgent(agent: string, project = home()?.id) {
  const info = await requestIn<AgentInfo>(project, 'agents.create', { agent })
  upsert(info, project)
  setActive(info.id)
  return info
}

export const catalog = (project = home()?.id) => requestIn<{ agents: CatalogAgent[]; local: boolean }>(project, 'agents.catalog')
export const installAgent = (agent: string) => requestIn<CatalogAgent[]>(home()?.id, 'agents.install', { agent })

// ---------- diffs ----------

/** Lines of a diff with 2 lines of context, gaps between the hunks. */
export function diffLines(oldText: string | null | undefined, newText: string): DiffLine[] {
  const a = oldText ? oldText.split('\n') : []
  const b = newText.split('\n')
  const hunks = lineHunks(a, b) ?? [{ a: [0, a.length], b: [0, b.length] }]
  const out: DiffLine[] = []
  let shown = 0 // lines of a already out
  const ctx = 2
  for (const h of hunks) {
    const from = Math.max(shown, h.a[0] - ctx)
    if (from > shown) out.push({ t: '…', text: 'line {n}', n: from + 1 })
    for (let i = from; i < h.a[0]; i++) out.push({ t: ' ', text: a[i] })
    for (let i = 0; i < h.a[1]; i++) out.push({ t: '-', text: a[h.a[0] + i] })
    for (let i = 0; i < h.b[1]; i++) out.push({ t: '+', text: b[h.b[0] + i] })
    shown = h.a[0] + h.a[1]
    const to = Math.min(a.length, shown + ctx)
    for (let i = shown; i < to; i++) out.push({ t: ' ', text: a[i] })
    shown = to
  }
  return out
}
