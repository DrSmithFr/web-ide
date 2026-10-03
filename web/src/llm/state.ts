// State of the AI assistant: model servers, models, and the current conversation. Kept at
// module level so that switching panels does not lose a running answer.
import { createSignal } from 'solid-js'
import { createStore, produce, reconcile } from 'solid-js/store'
import { on, request } from '../pod/rpc'

export interface ServerView {
  id: string
  name: string
  kind: 'auto' | 'llamacpp' | 'ollama'
  url: string
  hasKey: boolean
  context?: number
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
  kind: 'image' | 'video' | 'audio' | 'pdf' | 'text'
  size: number
  /** Small preview (data URL) for images and video frames. */
  thumb?: string
  note?: string
}

export interface Usage {
  prompt: number
  completion: number
  cached?: number
  perSecond?: number
  durationMs?: number
}

export interface ChatMessage {
  role: 'user' | 'assistant' | 'tool'
  content?: string | Part[]
  reasoning_content?: string
  tool_calls?: ToolCall[]
  tool_call_id?: string
  name?: string
  // Fields of the page only (removed before sending).
  attachments?: Attachment[]
  usage?: Usage
  error?: string
  /** Tool result: ok, error, or refused by the user. */
  status?: 'ok' | 'error' | 'denied'
  /** Short summary of a tool result shown folded. */
  summary?: string
  /** Diff of a file change, shown under the tool call. */
  diff?: DiffLine[]
  /** Model that wrote this answer (the model can change during a conversation). */
  model?: string
  /** Replaced by a summary: kept for display, not sent anymore. */
  compacted?: boolean
  /** Summary written by a compaction (role user), and the number of messages it replaces. */
  kind?: 'summary'
  summarized?: number
}

export interface DiffLine {
  t: ' ' | '+' | '-' | '…'
  text: string
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
}

export interface ChatInfo {
  id: string
  title: string
  updated: number
}

export const [config, setConfig] = createStore<{ servers: ServerView[]; server: string; model: string }>({ servers: [], server: '', model: '' })
export const [models, setModels] = createSignal<Model[]>([])
export const [modelsError, setModelsError] = createSignal('')
export const [modelsLoading, setModelsLoading] = createSignal(false)
export const [serverKind, setServerKind] = createSignal('')

export const [chat, setChat] = createStore<Chat>(emptyChat())
export const [chatList, setChatList] = createSignal<ChatInfo[]>([])

/** Answer being streamed. */
export const [live, setLive] = createStore({ busy: false, content: '', reasoning: '', tool: '', stream: '', compacting: false })

/** Edit waiting for the user (confirmation mode). */
export interface Approval {
  call: ToolCall
  path: string
  diff: DiffLine[]
  created: boolean
  resolve: (ok: boolean) => void
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
})
try {
  const p = JSON.parse(localStorage.getItem('webide.llm.prefs') ?? 'null')
  if (p) setPrefs(p)
} catch {
  /* private mode */
}
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
export async function loadConfig(force = false) {
  if (loaded && !force) return
  loaded = true
  const c = await request('llm.config')
  setConfig(reconcile(c))
  if (config.server) await loadModels()
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

export async function saveChat() {
  if (!chat.messages.length) return
  setChat('updated', Date.now())
  if (!chat.title) {
    const first = chat.messages.find((m) => m.role === 'user')
    const text = typeof first?.content === 'string' ? first.content : first?.content?.find((p) => p.type === 'text')?.text
    setChat('title', (text ?? first?.attachments?.[0]?.name ?? 'Conversation').replace(/\s+/g, ' ').trim().slice(0, 80))
  }
  try {
    await request('llm.chats.save', { chat: JSON.parse(JSON.stringify(chat)) })
    refreshChats()
  } catch {
    /* kept in memory */
  }
}

export async function openChat(id: string) {
  const c = await request<Chat>('llm.chats.get', { id })
  setChat(reconcile(c))
}

export function resetChat() {
  setChat(reconcile(emptyChat()))
}

export async function deleteChat(id: string) {
  await request('llm.chats.delete', { id })
  if (chat.id === id) resetChat()
  refreshChats()
}

export function pushMessage(m: ChatMessage) {
  setChat(produce((c) => c.messages.push(m)))
}

export function updateLast(fn: (m: ChatMessage) => void) {
  setChat(produce((c) => fn(c.messages[c.messages.length - 1])))
}
