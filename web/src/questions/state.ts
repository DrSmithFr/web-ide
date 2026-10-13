// Questions tool: every question waiting for the user in the projects of the window (pod:
// server/handlers_questions.go) — permissions and forms of the ACP sessions, ask_user of the
// assistant and of the CLI agents through MCP. The list is asked again on questions.changed.
// One question at a time shows in a modal; a new one opens it by itself unless the setting
// (or the override of its session) says no.
import { createRoot, createSignal } from 'solid-js'
import { on as onPod, request } from '../pod/rpc'
import { settings } from '../state/settings'
import type { Question as AskQuestion } from '../llm/state'
import { answerStrings, type AnswerEntry } from '../llm/ask'

export interface PermissionOption {
  optionId: string
  name: string
  kind: string
}

export interface Question {
  id: string
  source: 'acp' | 'assistant' | 'mcp'
  project: string
  session?: string
  index: number
  name: string
  title: string
  kind: 'permission' | 'form' | 'ask'
  text?: string
  options?: PermissionOption[]
  schema?: FormSchema
  questions?: AskQuestion[]
  last?: string
  time: number
  autoOpen?: boolean
}

export interface FormSchema {
  type?: string
  properties?: Record<string, FormField>
  required?: string[]
}

export interface FormField {
  type?: string
  title?: string
  description?: string
  enum?: string[]
  oneOf?: { const: string; title?: string; description?: string }[]
  items?: { enum?: string[]; anyOf?: { const: string; title?: string; description?: string }[] }
  minimum?: number
  maximum?: number
  default?: unknown
}

const [questions, setQuestions] = createSignal<Question[]>([])
const [shown, setShown] = createSignal<string | null>(null)
export { questions, shown }

// Questions seen by this window (a new one may open by itself), and those the user put off
// with Later (not opened by themselves again).
const seen = new Set<string>()
const later = new Set<string>()
let first = true

// Where the window shows questions already (the thread of a session or a conversation on
// screen): those do not pop up, their card is in view.
const visible = new Set<(q: Question) => boolean>()

/** Registers a part of the window that shows some questions; returns its removal. */
export function showsQuestions(f: (q: Question) => boolean) {
  visible.add(f)
  return () => visible.delete(f)
}

const autoOpens = (q: Question) => (q.autoOpen ?? settings.questions.autoOpen) && ![...visible].some((f) => f(q))

export async function refreshQuestions() {
  let list: Question[]
  try {
    list = await request<Question[]>('questions.list')
  } catch {
    return
  }
  setQuestions(list)
  const cur = shown()
  if (cur && !list.some((q) => q.id === cur)) {
    // Answered or withdrawn: the next one follows.
    setShown(list.find((q) => !later.has(q.id))?.id ?? null)
  }
  if (!shown() && !first) {
    const fresh = list.find((q) => !seen.has(q.id) && autoOpens(q))
    if (fresh) setShown(fresh.id)
  }
  for (const q of list) seen.add(q.id)
  first = false
}

let timer: number | undefined
createRoot(() => {
  onPod('questions.changed', () => {
    clearTimeout(timer)
    timer = window.setTimeout(() => void refreshQuestions(), 80)
  })
})

/** Shows a question in the modal (the first one waiting by default). */
export function showQuestion(id?: string) {
  const q = id ? questions().find((x) => x.id === id) : questions()[0]
  if (q) later.delete(q.id)
  setShown(q?.id ?? null)
}

/** Later: the modal closes; the question stays in the tool. */
export function putOff() {
  const cur = shown()
  if (cur) later.add(cur)
  setShown(null)
}

/** The windows' first load: the questions already waiting do not pop up. */
export function resetQuestions() {
  seen.clear()
  later.clear()
  first = true
  setShown(null)
  return refreshQuestions()
}

// ---------- answers ----------

export const answerPermission = (q: Question, option: string) => request('questions.answer', { id: q.id, option })
export const answerForm = (q: Question, action: 'accept' | 'decline' | 'cancel', content?: Record<string, unknown>) =>
  request('questions.answer', { id: q.id, action, content })

/** The answers of an ask_user card: to its conversation for the assistant, else to the MCP call. */
export function answerAsk(q: Question, answers: (AnswerEntry | null)[][], notes: string[], path?: number[], offPath?: number) {
  if (q.source === 'assistant') return request('agent.answer', { id: q.session, index: q.index, answers: answerStrings(answers), notes, path, offPath })
  return request('questions.answer', { id: q.id, answers: answerStrings(answers), notes, path, offPath })
}

export const skipAsk = (q: Question) => request('questions.answer', { id: q.id, skip: true })
