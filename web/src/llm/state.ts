// State of the AI assistant: model servers, models, and the current conversation. Kept at
// module level so that switching panels does not lose a running answer.
import { createSignal } from 'solid-js'
import { createStore, reconcile } from 'solid-js/store'
import { on, request } from '../pod/rpc'
import type { DoodleDoc } from './doodle/model'
import type { PreviewSpec } from './previews'

export interface ServerView {
  id: string
  name: string
  kind: 'auto' | 'llamacpp' | 'ollama' | 'openai'
  url: string
  hasKey: boolean
  context?: number
  /** Conversations the server runs at once (1 by default: one GPU). */
  parallel?: number
  /** Models typed by the user (a provider without /models, or to set capabilities). */
  models?: ModelConf[]
  /** Told to the model choosing a server for a sub-agent. */
  note?: string
  /** Offered to the sub-agents. */
  children?: boolean
}

export interface ModelConf {
  id: string
  context?: number
  tools: boolean
  vision: boolean
  thinking: boolean
}

export interface Caps {
  vision: boolean
  video: boolean
  audio: boolean
  tools: boolean
  thinking: boolean
  known: boolean
}

export interface Model {
  id: string
  size?: number
  details?: string
  state?: string
  context?: number
  caps: Caps
}

export interface ToolCall {
  id: string
  type: 'function'
  function: { name: string; arguments: string }
}

export type Part =
  | { type: 'text'; text: string }
  | { type: 'image_url'; image_url: { url: string } }
  | { type: 'input_audio'; input_audio: { data: string; format: string } }
  | { type: 'input_video'; input_video: { url: string } }

export interface Attachment {
  name: string
  kind: 'image' | 'video' | 'audio' | 'pdf' | 'text' | 'doodle'
  size: number
  /** Small preview (data URL) for images and video frames. */
  thumb?: string
  note?: string
  /** Vector document of a doodle (to open it again) and the description sent with it. */
  doodle?: DoodleDoc
  description?: string
  /** PNG of a doodle (data URL): the pod joins it to the tickets the model writes. */
  png?: string
  /** Size of an image as sent (its page on the board). */
  w?: number
  h?: number
}

export interface Usage {
  prompt: number
  completion: number
  cached?: number
  perSecond?: number
  durationMs?: number
  /** Speed of the prompt reading (tokens not found in the cache). */
  promptPerSecond?: number
}

export interface ChatMessage {
  role: 'user' | 'assistant' | 'tool'
  content?: string | Part[]
  reasoning_content?: string
  tool_calls?: ToolCall[]
  tool_call_id?: string
  name?: string
  // Fields of the page only (removed before sending).
  /** What the user typed (content also holds the text of the attachments). */
  display?: string
  /** 'claude': written or answered by Claude Code (MCP), else by the user. */
  author?: string
  attachments?: Attachment[]
  usage?: Usage
  error?: string
  /** Tool result: ok, error, or refused by the user. */
  status?: 'ok' | 'error' | 'denied'
  /** Short summary of a tool result shown folded: a text, or a text of the pod to translate. */
  summary?: string | SummaryText
  /** Diff of a file change, shown under the tool call. */
  diff?: DiffLine[]
  /** Model that wrote this answer (the model can change during a conversation). */
  model?: string
  /** Replaced by a summary: kept for display, not sent anymore. */
  compacted?: boolean
  /** Summary written by a compaction (role user), and the number of messages it replaces;
   *  the task of a sub-agent, an event of a sub-agent or of its parent, a reminder to report. */
  kind?: 'summary' | 'agent_task' | 'agent_event' | 'agent_nudge'
  summarized?: number
  /** Time spent thinking, and from the request to the end of the answer (ms). */
  thinkMs?: number
  elapsedMs?: number
  /** Mode in which an answer was written. */
  mode?: Mode
  /** Plan proposed with exit_plan_mode (tool message), and what the user did with it. */
  plan?: string
  planState?: 'pending' | 'accepted' | 'dismissed'
  /** Questions asked with ask_user (tool message), the answers, and their state. */
  questions?: Question[]
  answers?: string[][]
  /** Optional note the user adds to each question (one per question, '' when absent). */
  notes?: string[]
  /** A graph of questions: the questions asked, in order, and where the user left the path. */
  path?: number[]
  offPath?: number
  askState?: 'pending' | 'answered' | 'skipped'
  /** Page drawn on the board with board_draw (tool message), and the capture of the screen
   *  it waits for. */
  page?: ModelPage
  capture?: 'pending' | 'done' | 'refused' | 'skipped'
  /** App offered by share_preview (tool message): its card starts it. */
  preview?: PreviewSpec
  /** Event of a sub-agent in its parent, or of the parent in the child (kind agent_event). */
  event?: AgentEvent
  /** 'parent' on the result of agent_ask until the parent answers. */
  wait?: 'parent'
  /** Sub-agent started or addressed by this tool call of the parent. */
  child?: string
  /** Action offered by action_card (Orchestrator), and the conversation open_conversation
   *  moved the user into. */
  card?: ActionCard
  opened?: string
}

export interface ActionCard {
  kind: 'start_dev' | 'open_ticket' | 'generate_plan' | 'open_conversation'
  ticket?: number
  chat?: string
  label: string
  reason?: string
  state?: 'done' | 'failed'
  result?: string
}

/** Status of a sub-agent (pod/internal/agent/subagents.go). */
export type AgentStatus = 'running' | 'waiting_parent' | 'done' | 'blocked' | 'stopped' | 'error'

export interface SubAgent {
  task: string
  files?: string[]
  status: AgentStatus
  depth: number
  note?: { title: string; text: string }
  question?: string
  asked?: number
  report?: string
  changed?: string[]
  error?: string
  /** A conversation of its own followed by an Orchestrator (agent_adopt). */
  adopted?: boolean
}

export interface AgentEvent {
  child: string
  title: string
  type: 'note' | 'question' | 'report' | 'message'
  head?: string
  text: string
  status?: AgentStatus
  files?: string[]
  from?: string
}

/** A page of the board drawn by the model: its document, the description it read, images. */
export interface ModelPage {
  name: string
  doc: DoodleDoc
  description: string
  thumb?: string
  png?: string
}

/** A text written by the pod and translated here: t(key, params), or tn(n, key, other, params). */
export interface SummaryText {
  key: string
  other?: string
  n?: number
  params?: Record<string, any>
  prefix?: string
  suffix?: string
}

/** Kind of question: it picks the widget. Absent = "choice" (old calls). */
export type QuestionType = 'choice' | 'idea' | 'compare' | 'rank' | 'scenario'

export interface QuestionOption {
  label: string
  description?: string
  /** What goes for it / against it (choice and compare), shown as short ✓/✗ lines. */
  pros?: string[]
  cons?: string[]
  /** id of the question asked when this option is chosen (a graph of questions). */
  next?: string
}

export interface Question {
  question: string
  header?: string
  /** For "scenario": the concrete situation. */
  situation?: string
  /** For "rank": pick only the top N (N < number of options). */
  top?: number
  type?: QuestionType
  options: QuestionOption[]
  multiple?: boolean
  /** Graph of questions: the id others lead to; for an idea, the question after Yes / No. */
  id?: string
  nextYes?: string
  nextNo?: string
}

export interface DiffLine {
  t: ' ' | '+' | '-' | '…'
  /** For a gap ('…'), a text to translate with n ("line {n}"). */
  text: string
  n?: number
}

export interface Chat {
  id: string
  title: string
  created: number
  updated: number
  server: string
  model: string
  messages: ChatMessage[]
  /** Index from which reported usages count again (after a compaction). */
  resetAt?: number
  /** Set while the agent runs, with the stream of the completion awaited: a reloaded page resumes it. */
  running?: { stream?: string }
  /** Messages written during an answer, sent at the next step. */
  queue?: QueuedMessage[]
  /** Plan: explore and propose without changing files; Build (default): act. */
  mode?: Mode
  /** Kanban ticket this conversation works on, and its role (docs/kanban.md). */
  /** project: where the conversation works (the worktree of its ticket for a development). */
  ticket?: { id: number; role: ChatRole; feedback?: number; project?: string }
  /** Change or command waiting for the user. */
  approval?: Approval
  /** A sub-agent: the conversation that started it, and its task; the sub-agents started here. */
  parent?: string
  agent?: SubAgent
  /** Page only: the Orchestrator whose card started this conversation (sent with its first message). */
  adoptedBy?: string
  children?: string[]
  /** First message prepared by the Orchestrator, for the message box. */
  draft?: string
}

export type ChatRole = 'briefing' | 'plan' | 'dev' | 'correction' | 'resolve'

export type Mode = 'build' | 'plan' | 'briefing' | 'orchestrator'

export interface QueuedMessage {
  id: string
  text: string
  parts: Part[]
  attachments?: Attachment[]
  display?: string
}

export interface ChatInfo {
  id: string
  title: string
  updated: number
  model?: string
  /** A sub-agent: its parent and its status. */
  parent?: string
  status?: AgentStatus
  tokens?: number
  cost?: number
  mode?: Mode
  ticket?: number
}

export const [config, setConfig] = createStore<{ servers: ServerView[]; server: string; model: string; childServer?: string; childModel?: string }>({ servers: [], server: '', model: '' })
export const [models, setModels] = createSignal<Model[]>([])
export const [modelsError, setModelsError] = createSignal('')
export const [modelsLoading, setModelsLoading] = createSignal(false)
export const [serverKind, setServerKind] = createSignal('')

export const [chat, setChat] = createStore<Chat>(emptyChat())
export const [chatList, setChatList] = createSignal<ChatInfo[]>([])

/**
 * Answer being streamed, with its counters: request start, first token, reasoning span,
 * tokens so far, speed told by the server, progress of the prompt reading (tokens read,
 * total, tokens found in the cache, reading speed).
 */
export const [live, setLive] = createStore({
  /** The conversation shown runs in the pod (state other than idle). */
  busy: false,
  state: 'idle' as 'idle' | 'running' | 'queued' | 'waiting_user' | 'compacting',
  /** Queued: conversations before this one for the model server. */
  ahead: 0,
  content: '',
  reasoning: '',
  tool: '',
  stream: '',
  compacting: false,
  startedAt: 0,
  firstAt: 0,
  thinkStart: 0,
  thinkEnd: 0,
  tokens: 0,
  speed: 0,
  promptDone: 0,
  promptTotal: 0,
  promptCache: 0,
  promptSpeed: 0,
})

/** Live speed in tokens per second (server value, else measured since the first token). */
export function liveSpeed(now = Date.now()): number {
  if (live.speed) return live.speed
  if (!live.firstAt || live.tokens < 2) return 0
  const s = (now - live.firstAt) / 1000
  return s > 0.2 ? live.tokens / s : 0
}

/** Action waiting for the user: a file change (diff) or a command (Plan mode). */
export interface Approval {
  id: string
  call: ToolCall
  kind: 'edit' | 'command'
  path?: string
  diff?: DiffLine[]
  created?: boolean
  command?: string
}
export const [approval, setApproval] = createSignal<Approval | null>(null)

export const [prefs, setPrefs] = createStore({
  autoApply: false,
  think: true,
  tools: true,
  /** Local speech recognition: Whisper model and language. */
  whisperModel: 'base',
  whisperLang: 'auto',
  /** Send audio files as such to models that accept audio (else transcribed in the page). */
  audioToModel: false,
  /** Automatic compaction at compactAt % of the context, by the given model ('' = chat model). */
  autoCompact: true,
  compactAt: 75,
  compactServer: '',
  compactModel: '',
  /** History side bar open (when it is not always shown). */
  sidebarOpen: false,
  /** Board of the conversation: a column when the assistant is wide (with its share of the
   *  width), else shown instead of the conversation. */
  boardOpen: false,
  boardView: 'chat' as 'chat' | 'board',
  boardSplit: 0.5,
  /** Model of the Plan mode ('' server: the model of the conversation). */
  planServer: '',
  planModel: '',
  /** Mode of a new conversation. */
  defaultMode: 'orchestrator' as Mode,
})
try {
  const p = JSON.parse(localStorage.getItem('webide.llm.prefs') ?? 'null')
  if (p) setPrefs(p)
} catch {
  /* private mode */
}
/** A message prepared for the message box (the draft of a conversation opened for the user). */
export const [incomingDraft, setIncomingDraft] = createSignal('')

export function savePrefs() {
  try {
    localStorage.setItem('webide.llm.prefs', JSON.stringify(prefs))
  } catch {
    /* ignore */
  }
}

export function newId() {
  return Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
}

export function emptyChat(): Chat {
  const now = Date.now()
  return { id: newId(), title: '', created: now, updated: now, server: '', model: '', messages: [] }
}

export function currentModel(): Model | undefined {
  return models().find((m) => m.id === config.model)
}

/** Context size of the current model in tokens (0 when unknown). */
export function contextSize(): number {
  const server = config.servers.find((s) => s.id === config.server)
  if (serverKind() === 'ollama' && server?.context) return server.context
  return currentModel()?.context ?? 0
}

/** Rough token count of a message (images count for a fixed amount). */
export function estimateTokens(m: ChatMessage): number {
  let chars = 0
  let media = 0
  if (typeof m.content === 'string') chars += m.content.length
  else
    for (const p of m.content ?? []) {
      if (p.type === 'text') chars += p.text.length
      else media++
    }
  for (const c of m.tool_calls ?? []) chars += c.function.name.length + c.function.arguments.length
  return Math.ceil(chars / 3.5) + media * 800 + 8
}

/** Tokens the next request will use, from the last reported usage plus what came after. */
export function contextUsed(): number {
  const msgs = chat.messages
  let i = msgs.length - 1
  let extra = 0
  for (; i >= 0; i--) {
    const m = msgs[i]
    if (m.compacted) break
    // Usages reported before the last compaction counted the replaced messages.
    if (m.role === 'assistant' && m.usage && i >= (chat.resetAt ?? 0)) return m.usage.prompt + m.usage.completion + extra
    extra += estimateTokens(m)
  }
  return extra + 1500 // system prompt and tool definitions
}

let loaded = false
let loading: Promise<void> | null = null
/** Servers and models; later calls wait for the first load instead of skipping it. */
export function loadConfig(force = false): Promise<void> {
  if (loading && !force) return loading
  loaded = true
  loading = (async () => {
    const c = await request('llm.config')
    setConfig(reconcile(c))
    if (config.server) await loadModels()
  })()
  return loading
}

/** Configuration returned by the pod (servers saved or deleted from this window or another). */
export function applyConfig(c: any) {
  setConfig(reconcile(c))
}
on('llm.config', applyConfig)
on('pod.reconnected', () => {
  if (loaded) loadConfig(true).catch(() => {})
})

let modelsSeq = 0
export async function loadModels() {
  const seq = ++modelsSeq
  if (!config.server) {
    setModels([])
    return
  }
  setModelsLoading(true)
  setModelsError('')
  try {
    const r = await request<{ kind: string; models: Model[] }>('llm.models', { server: config.server })
    if (seq !== modelsSeq) return
    setModels(r.models)
    setServerKind(r.kind)
    if (!r.models.some((m) => m.id === config.model)) {
      const pick = r.models.find((m) => m.state === 'loaded') ?? r.models[0]
      await select(config.server, pick?.id ?? '')
    }
  } catch (e) {
    if (seq !== modelsSeq) return
    setModels([])
    setModelsError((e as Error).message)
  } finally {
    if (seq === modelsSeq) setModelsLoading(false)
  }
}

export async function select(server: string, model: string) {
  const changed = server !== config.server
  setConfig({ server, model })
  await request('llm.select', { server, model }).catch(() => {})
  if (changed) await loadModels()
}

// ---------- conversations ----------

export async function refreshChats() {
  try {
    setChatList(await request<ChatInfo[]>('llm.chats.list'))
  } catch {
    setChatList([])
  }
}

/** The window shows a new conversation (nothing in the pod until its first message). */
export function resetChat() {
  setChat(reconcile(emptyChat()))
  setApproval(null)
  request('agent.watch', { id: chat.id }).catch(() => {})
}

export async function renameChat(id: string, title: string) {
  await request('llm.chats.rename', { id, title })
  if (chat.id === id) setChat('title', title)
  refreshChats()
}

export async function deleteChat(id: string) {
  await request('llm.chats.delete', { id })
  if (chat.id === id) resetChat()
  refreshChats()
}

